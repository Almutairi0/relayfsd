import logging
import os
import sys
import time

import paramiko
from watchdog.events import FileSystemEventHandler
from watchdog.observers import Observer


def on_created(event):
    filepath = event.src_path
    print(f"Created: {filepath}")

    #Now we need to make sure the file is done Downloading
    last_size = -1
    while True:
        try:
            current_size = os.path.getsize(filepath)
        except:
            time.sleep(2)
            continue
        if current_size == last_size:
            break
            last_size = current_size
            time.sleep(2)

    print("file finished. now uploading")

    #Uplaod now

    ssh = paramiko.SSHClient()
    ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    ssh.connect(hostname = 'your ip',username = 'your username',password = 'password for the user',port=22)
    sftp_client = ssh.open_sftp() 
    sftp_client.put(filepath, f"'/To/your/des/{os.path.basename(filepath)}")
    sftp_client.close()
    ssh.close()
    print("upload complete")

if __name__ == "__main__":
    event_handler = FileSystemEventHandler()
    # Calling the function
    event_handler.on_created = on_created
    #Path
    path = "your/path"
    observer = Observer()
    observer.schedule(event_handler, path, recursive=True)
    observer.start()
    try:
        print("Monitoring")
        while True:
            time.sleep(1)
    finally:
        observer.stop()
         print("Done")
         observer.join()

