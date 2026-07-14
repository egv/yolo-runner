# Fix macFUSE for Arc: clean reinstall of 5.0.6 (2026-07-15)

## Why

- Two kernel panics on 2026-07-14/15, both inside the macFUSE kext
  (`io.macfuse.filesystems.macfuse.23`), triggered while processes walked arc
  FUSE mounts. Panic report: `/Library/Logs/DiagnosticReports/panic-full-2026-07-15-010206.0002.panic`.
- The install is internally inconsistent: the bundle Info.plist claims
  **5.0.6**, but the actual kext payload (and what the kernel loads, per
  `kmutil showloaded`) is **5.0.4** from June 2025 — the `Extensions/26`
  directory is a symlink to the old `14` kext. Rebooting can never fix this:
  a real 5.0.6 kext does not exist on disk.
- Internal Arc team guidance (docs + wiki, July 2026):
  - **5.0.6 is the validated version** for macOS Tahoe 26.x.
  - 4.9.1–5.0.3 and 5.0.7 are known-broken (vnode cache invalidation noop →
    stale files after `arc checkout`, ENXIO / "Device not configured").
  - 5.3.x is NOT validated internally — do not install it.
  - Sources:
    - https://docs.yandex-team.ru/arc/posts/2026-05-macfuse-broken-compat
    - https://wiki.yandex-team.ru/users/vlachistyakov/reshenie-problem-s-arc-i-versiejj-macfuse

## Procedure

### 1. Unmount everything arc has mounted

```bash
mount | grep -E "macfuse|arc" | awk '{print $3}'
# for each mount point:
arc unmount --force --forget <mount-point>
```

(Also quit anything that might hold the mounts: yolo-agent, Finder windows,
editors with files open on mounts.)

### 2. Remove macFUSE completely

```bash
sudo rm -rf /Library/Filesystems/macfuse.fs
sudo rm -rf /Library/Frameworks/macFUSE.framework
sudo rm -rf /Library/PreferencePanes/macFUSE.prefPane
sudo rm -rf /usr/local/include/fuse
sudo rm -rf /usr/local/lib/libfuse.*
sudo rm -rf /usr/local/lib/pkgconfig/fuse.pc
```

### 3. Reboot

```bash
sudo reboot
```

This clears the stale 5.0.4 kext from the staged auxiliary kext collection
(`/Library/StagedExtensions/...`).

### 4. Install genuine macFUSE 5.0.6

Download and install **exactly this version**:

https://github.com/macfuse/macfuse/releases/tag/macfuse-5.0.6

Run the `.dmg` installer.

### 5. Approve the system extension and reboot again

System Settings → Privacy & Security → allow the macFUSE system extension,
then reboot when the installer asks.

### 6. VERIFY (the step that was skipped last time)

```bash
# kext payload on disk must be 5.0.6, not just the bundle stamp:
defaults read "/Library/Filesystems/macfuse.fs/Contents/Extensions/26/macfuse.kext/Contents/Info" CFBundleShortVersionString
# expect: 5.0.6  (also check it is a real directory, not a symlink to 14/):
ls -la /Library/Filesystems/macfuse.fs/Contents/Extensions/

# after the first arc mount, the LOADED kext must be 5.0.6:
arc mount <some-repo>
kmutil showloaded | grep -i macfuse
# expect: io.macfuse.filesystems.macfuse.XX (5.0.6)
```

Do not trust the installer UI — only `kmutil showloaded` proves what the
kernel is running.

### 7. Update the arc client too

```bash
arc --set-channel stable && arc --update
arc --version
```

(Client was from 2026-05-25; the official post recommends stable channel.)

## If panics continue on a properly loaded 5.0.6

File a ticket in the **DEVTOOLSSUPPORT** queue with the panic file from
`/Library/Logs/DiagnosticReports/`. The 2026-07-15 panic frames were at
offsets `0xA1EC` and `0x6B68` inside the 5.0.4 kext, faulting address `0x50`
(NULL deref), victim process `find` walking an arc mount.

## Before restarting the yolo-runner swarm

- Re-verify macFUSE per step 6.
- The runner is fully stopped as of 2026-07-15 (~01:40 local): no yolo-agent
  processes, no PR mounts, status cron cancelled. In-flight queue items for
  "feat(swarm-generator): add Pro-channel churned-driver reactivation agent"
  (the embedded-tools-sync fix and follow-ups) are parked in
  `.yolo-runner/watch.db` and resume only when `yolo-agent watch` is started
  manually:

  ```bash
  cd ~/dev/yolo-runner
  env -u ARC_TOKEN ./bin/yolo-agent watch --repo . \
    --environments ~/.yolo-runner/environments.yaml \
    --events "runner-logs/watch-$(date +%Y%m%d_%H%M%S).events.jsonl"
  ```

- Longer-term hardening ideas (defense in depth, tracked as task chips):
  reuse one long-lived mount instead of per-PR mount churn; kill agent child
  processes and wait for open handles before unmounting; consider running the
  swarm on a Linux host (remote codex hosts exist: oholiab, bezalel, codenv).
