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

### Migrating from password authentication

Existing configs with no `auth_method` keep using password authentication. To switch, follow the steps above. Choosing `key` in the wizard removes the stored password from `data.json`.

If authentication fails, Relayfsd logs which method it tried and the most likely fix. For key auth, check that the public key is in `~/.ssh/authorized_keys` on the server.

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
