![Go](https://img.shields.io/badge/go-1.19+-blue?logo=go)
![AUR](https://img.shields.io/badge/AUR-available-orange)
![License](https://img.shields.io/badge/license-MIT-lightgrey)

A lightweight tool that monitors a directory and automatically uploads new or completed files to a Linux server via SFTP.

---

## Features

- Monitors any folder for new files
- Automatically uploads files to a Linux server
- Runs continuously in the background
- Lightweight and easy to configure
- Log events to a file
- Send notifications (Discord support)
- The ability to transfer files from a remote system to the local machine.
- SSH key authentication
- Server identity checked against `known_hosts` (protects against man-in-the-middle)
- Optionally move or delete local files after upload
---

## Installation

### Arch Linux (AUR)

If you use Arch BTW !, you can install directly from AUR:

```bash
yay -S relayfsd
```

### Manual Installation

1. Clone the repository:
```bash
git clone https://github.com/Almutairi0/relayfsd.git
cd relayfsd
```

2. Navigate to the src directory and build:
```bash
cd src
go build
```

3. Run the application:
```bash
go run . 
```

For first-time setup, configure with:
```bash
go run . --config
```

---

## Configuration

Edit the configuration file to set your server details, watch directory, and destination directory path.

### SSH key authentication (recommended)

Relayfsd can log in with an SSH key pair instead of a password.

1. Generate a key pair on the local machine (skip this if you already have one):
```bash
ssh-keygen -t ed25519
```

2. Copy the public key to the server:
```bash
ssh-copy-id user@server-ip
```

3. Check that key login works:
```bash
ssh user@server-ip
```

4. Run `./relayfsd --config`, choose `key`, and enter the path to your private key (default `~/.ssh/id_ed25519`). If the key has a passphrase, enter it when prompted.

The resulting `data.json` contains:
```json
"auth_method": "key",
"key_path": "~/.ssh/id_ed25519"
```

### Server identity (host key verification)

Before logging in, Relayfsd checks the server's host key against `~/.ssh/known_hosts`, the same file `ssh` uses. This stops a man-in-the-middle from receiving your password or your files.

- **Unknown server:** Relayfsd refuses to start until you trust it.
- **Changed key:** Relayfsd refuses to connect, logs an error and sends one Discord alert. This also applies if the key is marked `@revoked`, or if the server suddenly offers a different key type than the one recorded.

To trust the server, run `./relayfsd --config`. After you enter the IP, it shows the server's key fingerprint:

```
The server presented this host key:
  ssh-ed25519  SHA256:Xk3p9...q7Lw
Trust this server? (y/N):
```

To make sure it's genuine, run this **on the server itself** and check that the fingerprints match:

```bash
ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub
```

If you've already connected with `ssh user@server-ip` and accepted the key, the server is already trusted.

If you reinstall the server, its key changes. Remove the old entry and trust the new one:

```bash
ssh-keygen -R server-ip
./relayfsd --config
```

To use a different file, set `"known_hosts_path"` in `data.json` (and add `-f <path>` to the `ssh-keygen -R` command above).

Lines Relayfsd can't parse, such as newer key types, are skipped with a warning in the log. Host certificates (`@cert-authority`) aren't supported. If your server is listed only in one of those ways, `--config` warns you and asks you to type `yes` before trusting its plain key, so compare the fingerprint on the server first.

> **Upgrading:** earlier versions did not check the host key. After upgrading, Relayfsd won't start until you trust the server once with `./relayfsd --config` (or `ssh user@server-ip`).

### Migrating from password authentication

Existing configs with no `auth_method` keep using password authentication. To switch, follow the steps above. Choosing `key` in the wizard removes the stored password from `data.json`.

If authentication fails, Relayfsd logs which method it tried and the most likely fix. For key auth, check that the public key is in `~/.ssh/authorized_keys` on the server.

### After upload: keep, move or delete local files

When uploading local files to the server, Relayfsd can clean up after itself. Choose this in `./relayfsd --config`, or set it in `data.json`:

```json
"after_upload": "move",
"move_dir": "/home/user/uploaded"
```

| `after_upload` | Effect |
|---|---|
| `keep` (default) | Local files are left untouched |
| `move` | Files are moved to `move_dir`. Name clashes get a ` (1)` suffix |
| `delete` | Files are permanently deleted |

Safeguards:
- A file is only moved or deleted after the remote copy is verified to be the same size.
- If the file changed during the upload (for example, it was still being written), it is left in place.
- `move_dir` must be outside the watch folder, otherwise moved files would be uploaded again.
- The wizard asks you to type `yes` before enabling `delete`.
- Every move, delete, skip and failure is written to `relayfsd.log`. Failures also trigger a Discord notification.

---

## Usage

```bash
cd relayfsd/src
go build
go run . 
```

For first-time setup:
```bash
go run . --config
```

---

## License

MIT License - See LICENSE file for details
