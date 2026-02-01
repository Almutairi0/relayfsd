import json
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

    except Exception:
        logging.exception("Upload failed for %s", filepath)
    

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

