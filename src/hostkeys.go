package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const defaultKnownHosts = "~/.ssh/known_hosts"

// Errors returned when the server's host key can't be trusted. Both stop
// the connection before any password or key is sent to the server.
var (
	errHostUnknown     = errors.New("host key not trusted")
	errHostKeyRejected = errors.New("HOST KEY REJECTED")
)

// plainHostKeyAlgos are the host key algorithms the wizard asks for when
// the server isn't known yet, most preferred first. Certificates are left
// out on purpose: relayfsd records and checks plain keys only. Ed25519
// comes first so the fingerprint shown matches what ssh(1) records.
var plainHostKeyAlgos = []string{
	ssh.KeyAlgoED25519,
	ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
	ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256,
}

// knownHostsPath returns the known_hosts file in use, with ~ expanded.
func knownHostsPath() string {
	if cfg.KnownHostsPath != "" {
		return expandHome(cfg.KnownHostsPath)
	}
	return expandHome(defaultKnownHosts)
}

// sshAddr is the address relayfsd connects to. Hostnames are lowercased,
// as ssh(1) does when writing known_hosts, so "MyHost" matches "myhost".
func sshAddr() string {
	return net.JoinHostPort(strings.ToLower(cfg.IP), "22")
}

// hostDB is the parsed known_hosts file, seen from the configured server.
type hostDB struct {
	exists bool
	// check verifies a plain host key. @cert-authority lines are left
	// out, since relayfsd doesn't accept host certificates.
	check ssh.HostKeyCallback
	// known holds the plain keys recorded for the server.
	known []ssh.PublicKey
	// unverifiable is set when the server is listed, but only by lines
	// relayfsd can't use: @cert-authority, or keys it can't parse. ssh(1)
	// knows the server, so a key it presents is not simply "new".
	unverifiable bool
}

// maxKnownHostsLine is the longest line the knownhosts package can read.
const maxKnownHostsLine = 60 * 1024

// loadHostDB reads known_hosts in one pass. Lines relayfsd can't use are
// skipped with a warning, like ssh(1) does, instead of making the whole
// file unusable.
func loadHostDB() (*hostDB, error) {
	path := knownHostsPath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &hostDB{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", path, err)
	}

	// usable: lines the knownhosts package will accept, minus CA lines.
	// shadow: the host field of every line we can't verify with, paired
	// with a dummy key, to find out whether one of them names our server.
	var usable, shadow []string
	var usableNums []int // real line number of each usable line
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		if len(line) > maxKnownHostsLine {
			warnOnce(fmt.Sprintf("skipping %s line %d: line is too long", path, i+1))
			continue
		}
		marker, host, err := parseKnownHostsLine(line)
		switch {
		case err != nil:
			warnOnce(fmt.Sprintf("skipping %s line %d: %v", path, i+1, err))
			if marker != "@revoked" && validHostField(host) {
				shadow = append(shadow, host)
			}
		case marker == "@cert-authority":
			shadow = append(shadow, host)
		default:
			usable = append(usable, line)
			usableNums = append(usableNums, i+1)
		}
	}

	db := &hostDB{exists: true}
	if db.check, err = parseUsable(path, usable, usableNums); err != nil {
		return nil, err
	}
	if db.known, err = keysFor(db.check); err != nil {
		return nil, err
	}
	if len(db.known) == 0 && len(shadow) > 0 {
		probe, err := shadowLines(shadow)
		if err != nil {
			return nil, err
		}
		shadowCheck, err := knownHostsFromLines(probe)
		if err != nil {
			return nil, fmt.Errorf("could not read %s: %w", path, err)
		}
		listed, err := keysFor(shadowCheck)
		if err != nil {
			return nil, err
		}
		db.unverifiable = len(listed) > 0
	}
	return db, nil
}

// parseKnownHostsLine checks a line the same way the knownhosts package
// does, so that a line passing here never makes knownhosts.New fail. It
// returns the marker, and the host field when the line has a key field,
// even if that key is unusable.
func parseKnownHostsLine(line string) (marker, host string, err error) {
	if w, rest := nextWord(line); w == "@cert-authority" || w == "@revoked" {
		marker, line = w, rest
	}
	host, line = nextWord(line)
	if line == "" {
		return marker, "", errors.New("missing key type")
	}
	_, line = nextWord(line) // the type is ignored; the key blob has it
	if line == "" {
		return marker, "", errors.New("missing key")
	}
	keyText, _ := nextWord(line)

	blob, err := base64.StdEncoding.DecodeString(keyText)
	if err != nil {
		return marker, host, fmt.Errorf("bad key encoding: %w", err)
	}
	if _, err := ssh.ParsePublicKey(blob); err != nil {
		return marker, host, err
	}
	if marker != "@revoked" && !validHostField(host) {
		return marker, "", fmt.Errorf("bad host pattern %q", host)
	}
	return marker, host, nil
}

// nextWord splits off the first word exactly as the knownhosts package
// does: only space and tab separate words, so e.g. a no-break space is
// part of a word there too.
func nextWord(line string) (word, rest string) {
	i := strings.IndexAny(line, " \t")
	if i == -1 {
		return line, ""
	}
	return line[:i], strings.TrimSpace(line[i:])
}

// validHostField reports whether knownhosts accepts host as a pattern.
func validHostField(host string) bool {
	if host == "" {
		return false
	}
	if host[0] == '|' {
		parts := strings.Split(host, "|")
		if len(parts) != 4 || parts[1] != "1" {
			return false
		}
		_, err1 := base64.StdEncoding.DecodeString(parts[2])
		_, err2 := base64.StdEncoding.DecodeString(parts[3])
		return err1 == nil && err2 == nil
	}
	for _, p := range strings.Split(host, ",") {
		if p == "" {
			continue
		}
		p = strings.TrimPrefix(p, "!")
		if p == "" {
			return false
		}
		if p[0] == '[' {
			if _, _, err := net.SplitHostPort(p); err != nil {
				return false
			}
		}
	}
	return true
}

// shadowLines pairs each host field with a throwaway key.
func shadowLines(hosts []string) ([]string, error) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	keyText := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	lines := make([]string, len(hosts))
	for i, h := range hosts {
		lines[i] = h + " " + keyText
	}
	return lines, nil
}

// parseUsable loads the pre-checked lines. If knownhosts still rejects one
// (parseKnownHostsLine should prevent that), it is skipped and the rest is
// tried again, so one odd line can't make the whole file unusable.
func parseUsable(path string, lines []string, nums []int) (ssh.HostKeyCallback, error) {
	const maxRetries = 20
	for retry := 0; ; retry++ {
		check, err := knownHostsFromLines(lines)
		if err == nil {
			return check, nil
		}
		m := knownHostsErrLine.FindStringSubmatch(err.Error())
		if m == nil || retry == maxRetries {
			return nil, fmt.Errorf("could not read %s: %w", path, err)
		}
		n, _ := strconv.Atoi(m[1])
		if n < 1 || n > len(lines) {
			return nil, fmt.Errorf("could not read %s: %w", path, err)
		}
		warnOnce(fmt.Sprintf("skipping %s line %d: %s", path, nums[n-1], m[2]))
		lines = slices.Delete(slices.Clone(lines), n-1, n)
		nums = slices.Delete(slices.Clone(nums), n-1, n)
	}
}

// knownHostsErrLine matches knownhosts parse errors for the pipe used by
// knownHostsFromLines: "knownhosts: /dev/fd/<fd>:<line>: <reason>".
var knownHostsErrLine = regexp.MustCompile(`^knownhosts: /dev/fd/\d+:(\d+): (.*)$`)

// knownHostsFromLines parses already-validated lines. knownhosts.New only
// reads named files, so the lines are fed through a pipe opened by its
// /dev/fd name; nothing is written to disk.
func knownHostsFromLines(lines []string) (ssh.HostKeyCallback, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	go func() {
		w.WriteString(strings.Join(lines, "\n"))
		w.Close()
	}()
	return knownhosts.New(fmt.Sprintf("/dev/fd/%d", r.Fd()))
}

// keysFor returns the keys check has recorded for the server. Checking a
// throwaway key makes knownhosts list every key for the host in
// KeyError.Want.
func keysFor(check ssh.HostKeyCallback) ([]ssh.PublicKey, error) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	probe, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, err
	}

	var keyErr *knownhosts.KeyError
	if !errors.As(check(sshAddr(), probeAddr(), probe), &keyErr) {
		return nil, nil
	}
	keys := make([]ssh.PublicKey, len(keyErr.Want))
	for i, k := range keyErr.Want {
		keys[i] = k.Key
	}
	return keys, nil
}

// algorithms returns the host key algorithms for the recorded keys, in
// plainHostKeyAlgos order. Restricting the handshake to these makes the
// server present a key we can actually check, instead of e.g. an ed25519
// key when only its ecdsa key was recorded.
func (db *hostDB) algorithms() []string {
	have := map[string]bool{}
	for _, k := range db.known {
		have[k.Type()] = true
	}
	var algos []string
	for _, algo := range plainHostKeyAlgos {
		keyType := algo
		if algo == ssh.KeyAlgoRSASHA512 || algo == ssh.KeyAlgoRSASHA256 {
			keyType = ssh.KeyAlgoRSA
		}
		if have[keyType] {
			algos = append(algos, algo)
		}
	}
	return algos
}

// hostKeyConfig builds the host key checks for one connection. known_hosts
// is re-read every time, so a host approved with ssh(1) or --config while
// the daemon runs is picked up without a restart.
func hostKeyConfig() (ssh.HostKeyCallback, []string, error) {
	path := knownHostsPath()
	db, err := loadHostDB()
	if err != nil {
		return nil, nil, err
	}
	switch {
	case !db.exists:
		return nil, nil, fmt.Errorf("%w: %s does not exist; %s", errHostUnknown, path, trustHint())
	case len(db.known) == 0 && db.unverifiable:
		return nil, nil, fmt.Errorf("%w: %s is listed in %s only through @cert-authority or a key "+
			"relayfsd can't read; %s", errHostUnknown, cfg.IP, path, trustHint())
	}

	algos := db.algorithms()
	if len(algos) == 0 {
		if len(db.known) > 0 {
			return nil, nil, fmt.Errorf("%w: %s has no key type relayfsd can use for %s", errHostUnknown, path, cfg.IP)
		}
		return nil, nil, fmt.Errorf("%w: %s is not in %s; %s", errHostUnknown, cfg.IP, path, trustHint())
	}

	callback := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := db.check(hostname, remote, key)
		var keyErr *knownhosts.KeyError
		var revoked *knownhosts.RevokedError
		switch {
		case errors.As(err, &revoked):
			return fmt.Errorf("%w for %s: server sent %s %s, which is marked @revoked in %s. "+
				"This could be a man-in-the-middle attack",
				errHostKeyRejected, cfg.IP, key.Type(), ssh.FingerprintSHA256(key), path)
		case errors.As(err, &keyErr) && len(keyErr.Want) > 0:
			return fmt.Errorf("%w for %s: the host key has CHANGED. Server sent %s %s, which does not match %s. "+
				"This could be a man-in-the-middle attack. If you reinstalled the server, %s",
				errHostKeyRejected, cfg.IP, key.Type(), ssh.FingerprintSHA256(key), path, retrustHint())
		}
		return err
	}
	return callback, algos, nil
}

// classifyDialError turns a handshake that failed because the server
// offered none of the recorded key types into a host key rejection. A real
// server doesn't drop its keys, so this looks like an impostor.
func classifyDialError(err error) error {
	var negErr *ssh.AlgorithmNegotiationError
	if errors.As(err, &negErr) && negErr.What == "host key" {
		return fmt.Errorf("%w for %s: the server offers none of the host key types recorded in %s "+
			"(we wanted %v, it offered %v). This could be a man-in-the-middle attack. If you "+
			"reinstalled the server, %s",
			errHostKeyRejected, cfg.IP, knownHostsPath(), negErr.SupportedAlgorithms, negErr.RequestedAlgorithms, retrustHint())
	}
	return err
}

// probeAddr is the remote address passed when checking known_hosts
// without a real connection.
func probeAddr() net.Addr {
	if ip := net.ParseIP(cfg.IP); ip != nil {
		return &net.TCPAddr{IP: ip, Port: 22}
	}
	return &net.TCPAddr{Port: 22}
}

func trustHint() string {
	return fmt.Sprintf("run 'relayfsd --config' to check and trust the server's key (or 'ssh %s@%s' once)",
		cfg.Username, cfg.IP)
}

// removeCommand is the ssh-keygen command that forgets the server's keys.
func removeCommand() string {
	if cfg.KnownHostsPath != "" {
		return fmt.Sprintf("ssh-keygen -R %s -f %s", strings.ToLower(cfg.IP), knownHostsPath())
	}
	return fmt.Sprintf("ssh-keygen -R %s", strings.ToLower(cfg.IP))
}

func retrustHint() string {
	return fmt.Sprintf("run '%s' and then 'relayfsd --config' to trust the new key", removeCommand())
}

// isHostKeyError reports whether err means the server's identity could
// not be verified, as opposed to it being offline or rejecting the login.
func isHostKeyError(err error) bool {
	return errors.Is(err, errHostKeyRejected) || errors.Is(err, errHostUnknown)
}

// Alert state for rejected host keys. Each distinct incident is announced
// once, not on every retry; a successful connection ends the incident.
var (
	alertMu   sync.Mutex
	lastAlert string
)

// reportHostKeyError logs and alerts when a connection failed because the
// host key could not be trusted. It returns true if err was such a failure.
// Transfers fail until the problem is fixed, so both kinds are alerted.
func reportHostKeyError(err error) bool {
	if !isHostKeyError(err) {
		return false
	}

	alertMu.Lock()
	isNew := err.Error() != lastAlert
	lastAlert = err.Error()
	alertMu.Unlock()

	if !isNew {
		return true
	}
	logger.Printf("ERROR | %v", err)
	if errors.Is(err, errHostKeyRejected) {
		notifyDiscord(fmt.Sprintf("⚠️ SSH host key for %s was REJECTED and Relayfsd refused to connect. "+
			"This could be a man-in-the-middle attack. Check the server before trusting a new key. "+
			"Details are in relayfsd.log.", cfg.IP))
	} else {
		notifyDiscord(fmt.Sprintf("⚠️ Relayfsd no longer trusts %s: it is not usable in %s. "+
			"Transfers will fail until you run 'relayfsd --config'. Details are in relayfsd.log.",
			cfg.IP, knownHostsPath()))
	}
	return true
}

// hostKeyAccepted ends the current host key incident, so a later one is
// alerted again.
func hostKeyAccepted() {
	alertMu.Lock()
	lastAlert = ""
	alertMu.Unlock()
}

// warnings already printed, so skipped known_hosts lines are reported
// once per run instead of on every connection.
var (
	warnMu sync.Mutex
	warned = map[string]bool{}
)

func warnOnce(msg string) {
	warnMu.Lock()
	defer warnMu.Unlock()
	if warned[msg] {
		return
	}
	warned[msg] = true
	if logger != nil {
		logger.Printf("WARN | %s", msg)
	} else {
		fmt.Printf("⚠️  %s\n", msg)
	}
}

// hostKeyStatus describes how the server's key compares with known_hosts.
type hostKeyStatus int

const (
	hostKeyTrusted  hostKeyStatus = iota
	hostKeyUnknown                // not recorded; safe to offer trusting it
	hostKeyMismatch               // recorded with a different key
	hostKeyNoCommon               // recorded, but the server offers none of those key types
	hostKeyRevoked                // the key is marked @revoked
	// listed only by @cert-authority or unparseable lines: ssh(1) knows
	// the server, but relayfsd can't verify this key
	hostKeyUnverifiable
)

// inspectHostKey connects without logging in, gets the server's host key
// and compares it with known_hosts. No credentials are sent.
//
// If known_hosts already has keys for the server, only those key types are
// requested, so the result is compared like for like. Otherwise an impostor
// could present a different key type and look merely "unknown".
func inspectHostKey() (ssh.PublicKey, hostKeyStatus, error) {
	db, err := loadHostDB()
	if err != nil {
		return nil, 0, err
	}
	algos := plainHostKeyAlgos
	if len(db.known) > 0 {
		if algos = db.algorithms(); len(algos) == 0 {
			return nil, 0, fmt.Errorf("%s has no key type relayfsd can use for %s", knownHostsPath(), cfg.IP)
		}
	}

	var got ssh.PublicKey
	errGotKey := errors.New("got host key")
	sshCfg := &ssh.ClientConfig{
		User: cfg.Username,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got = key
			return errGotKey // abort before authentication
		},
		HostKeyAlgorithms: algos,
		Timeout:           10 * time.Second,
	}
	_, err = ssh.Dial("tcp", sshAddr(), sshCfg)
	if got == nil {
		var negErr *ssh.AlgorithmNegotiationError
		if len(db.known) > 0 && errors.As(err, &negErr) && negErr.What == "host key" {
			return nil, hostKeyNoCommon, nil
		}
		return nil, 0, fmt.Errorf("could not get host key from %s: %w", cfg.IP, err)
	}
	if _, isCert := got.(*ssh.Certificate); isCert {
		// Can't happen with plain algorithms only, but never record one.
		return nil, 0, fmt.Errorf("server at %s sent a host certificate, which relayfsd does not support", cfg.IP)
	}
	if !db.exists {
		return got, hostKeyUnknown, nil
	}

	err = db.check(sshAddr(), probeAddr(), got)
	var keyErr *knownhosts.KeyError
	var revoked *knownhosts.RevokedError
	switch {
	case err == nil:
		return got, hostKeyTrusted, nil
	case errors.As(err, &revoked):
		return got, hostKeyRevoked, nil
	case errors.As(err, &keyErr) && len(keyErr.Want) == 0 && db.unverifiable:
		return got, hostKeyUnverifiable, nil
	case errors.As(err, &keyErr) && len(keyErr.Want) == 0:
		return got, hostKeyUnknown, nil
	case errors.As(err, &keyErr):
		return got, hostKeyMismatch, nil
	default:
		return nil, 0, err
	}
}

// trustHostKey appends key for the server to known_hosts, creating the
// file (and ~/.ssh) with private permissions if needed.
func trustHostKey(key ssh.PublicKey) error {
	host := knownhosts.Normalize(sshAddr())
	if host == "" || strings.TrimSpace(cfg.IP) == "" {
		return errors.New("no server address set")
	}

	path := knownHostsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	line := knownhosts.Line([]string{host}, key) + "\n"
	// Don't glue our entry onto a last line that lacks its newline.
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		line = "\n" + line
	}
	if _, err := f.WriteString(line); err != nil {
		return err
	}
	return f.Close()
}
