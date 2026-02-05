# TorrentSync

![Python](https://img.shields.io/badge/python-3.8+-blue?logo=python)
![Watchdog](https://img.shields.io/badge/watchdog-monitored-green)
![Paramiko](https://img.shields.io/badge/paramiko-SFTP-orange)
![License](https://img.shields.io/badge/license-MIT-lightgrey)

A lightweight Python tool that monitors a Windows download folder (such as a torrent directory) and automatically uploads completed files to a Linux server via SFTP.

It uses:

- **Watchdog** — detects new files in real time  
- **Paramiko** — handles SFTP file transfer 

---

## Features

-  Monitors any folder for new files   
-  Automatically uploads files to a Linux server  
-  Runs continuously in the background  
-  Lightweight and easy to customize
-  Log events to a file instead of printing to console.

---

## Setup, Configuration & Usage

Install dependencies:

```bash
pip install watchdog paramiko
```
**Create your config file:**
- Copy data.example.json to data.json
- Edit data.json and set your values:
```
{
  "ip": "192.168.1.100",
  "username": "your_ssh_username",
  "password": "your_ssh_password",
  "watch_path": "R:/Torrent/Completed",
  "remote_dir": "/DATA/Media/TV"
}
```


---
## Run the script
```bash
python main.py
```
The script will monitor the folder and the log will be like:
```console
2026-02-05 13:02:11,184 | INFO | Monitoring
2026-02-05 13:05:42,903 | INFO | Found: R:\Torrent\New\blalba.S01E01.mkv
2026-02-05 13:05:42,904 | INFO | Now Uploading
2026-02-05 13:06:58,221 | INFO | upload complete
2026-02-05 13:10:33,990 | INFO | Found: R:\Torrent\New\Blabla.s01e02.mkv
2026-02-05 13:10:33,991 | INFO | Now Uploading
2026-02-05 13:12:04,558 | INFO | upload complete
```
