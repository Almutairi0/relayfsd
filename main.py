import json
import requests
import logging
import os
import sys
import time

import paramiko
from watchdog.events import FileSystemEventHandler
from watchdog.observers import Observer

logging.basicConfig(
    filename="torrentsync.log",
    level=logging.INFO,
    format="%(asctime)s | %(levelname)s | %(message)s",
)

with open('data.json', 'r') as json_file:
    data = json.load(json_file)


def notify_discord(message: str):
    discord_cfg = data.get("notifications", {}).get("discord", {})

    if not discord_cfg.get("enabled"):
        return

    webhook_url = discord_cfg.get("webhook_url")
    if not webhook_url:
        logging.warning("Discord notifications enabled but webhook_url is missing")
        return

    payload = {
        "content": message
    }

    try:
        response = requests.post(webhook_url, json=payload, timeout=5)
        if response.status_code not in (200, 204):
            logging.error(f"Discord notification failed: {response.status_code}")
    except Exception as e:
        logging.error(f"Discord notification error: {e}")


def on_created(event):
    if event.is_directory:
        return
    filepath = event.src_path
    logging.info(f"Found: {filepath}")
    
    logging.info("Now Uploading")
    try:
        ssh = paramiko.SSHClient()
        ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        ssh.connect(
            hostname=data["ip"],
            username=data["username"],
            password=data["password"],
            port=22
        )

        sftp = ssh.open_sftp()
        sftp.put(filepath, f'{data["remote_dir"]}/{os.path.basename(filepath)}')
        sftp.close()
        ssh.close()

        logging.info("Upload complete")
        notify_discord(
            f" Uploaded: {os.path.basename(filepath)} → {data['remote_dir']}"
        )


    except Exception as e:
        logging.exception(f"Upload failed for {filepath}: {e}")
        notify_discord(
        f" Upload failed: {os.path.basename(filepath)}\nReason: {e}"
        )
    

if __name__ == "__main__":
    event_handler = FileSystemEventHandler()
    # Calling the function
    event_handler.on_created = on_created
    #Path
    watch_path = data["watch_path"]
    observer = Observer()
    observer.schedule(event_handler, watch_path, recursive=True)
    observer.start()
    try:
        logging.info("Monitoring")
        while True:
            time.sleep(1)
    finally:
        observer.stop()
        logging.info("Done")
        observer.join()

