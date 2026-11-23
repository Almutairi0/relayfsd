# TODO

## Retry failed uploads (local → remote)

Today a failed upload (server down, network drop, full disk, wrong
`remote_dir`) is logged once with one Discord alert, then never tried again
unless the file is written to. Downloads (remote → local) already retry on
the next poll.

- [ ] **Retry queue in memory.** Retry a failed file after 10s, 20s, 40s, …
      up to every 5 minutes, like the remote watcher's backoff.
      - When any upload succeeds, retry everything waiting right away.
      - Drop files deleted while waiting; files that change are picked up
        normally.
      - Discord: one alert on the first failure, one "uploaded after N
        retries" on success, not one per attempt.
      - UI: a "waiting to retry" state; the Failed count counts files, not
        attempts.
      - To decide: retry forever while the file exists, or give up after a
        while (e.g. 24h)? A reminder alert if a file is stuck for an hour?
- [ ] **Startup catch-up (opt-in, e.g. `"startup_scan": true`).** Files
      added while the daemon wasn't running are never uploaded. On start,
      compare the watch folder with `remote_dir` and upload files missing
      there or with a different size. Opt-in, because with
      `after_upload: keep` the first run could upload many old files, and a
      file deleted on the server on purpose would come back.
- [ ] Remove the "A failed upload is not retried" note from the BUGS
      section of `relayfsd.1` once the retry queue is in.
