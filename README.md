# TorrentSync

![Python](https://img.shields.io/badge/python-3.8+-blue?logo=python)
![Watchdog](https://img.shields.io/badge/watchdog-monitored-green)
![Paramiko](https://img.shields.io/badge/paramiko-SFTP-orange)
![License](https://img.shields.io/badge/license-MIT-lightgrey)

A lightweight Python tool that monitors a Windows download folder (such as a torrent directory) and automatically uploads completed files to a Linux server via SFTP. It ensures the file is fully downloaded before uploading.

It uses:

- **Watchdog** — detects new files in real time  
- **Paramiko** — handles SFTP file transfer 

---

## Features

-  Monitors any folder for new files   
-  Automatically uploads files to a Linux server  
-  Runs continuously in the background  
-  Lightweight and easy to customize  

---

## Setup, Configuration & Usage

Install dependencies:

```bash
pip install watchdog paramiko
```
---
## Must configure this !!!

- path = "Your completed torrent file"          **# competed torrent folder to monitor**
- hostname = 'put your ip'                      **# Linux server IP**
- username = 'put your username'"               **# SSH username**
- password = 'the password for username'        **# SSH password**

---
## Run the script
```bash
python main.py
```
The script will monitor the folder and print messages like:
```console
Monitoring
Found: vid.mp4
Now Uploading ..
upload complete
```
---
## If you want to contribute fell free to add this improvements

- Log events to a file instead of printing to console

- Use SSH keys instead of passwords for secure authentication

- Move or delete files locally after upload

- Add a configuration file (config.json)

- Send notifications (Telegram/Discord) after upload
