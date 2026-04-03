# Release Playbook for v2.6.1+

## Preflight

- Ensure release candidates start from a clean tree: `git status --short`
- Run tests: `go test ./...`
- Build artifacts: `go build ./...`
- Run the E8 release gate when required for this branch (`make release-gate-e8`).

## Tagging

Use the following commands for the v2.6.1 release tag:

```bash
git tag -a v2.6.1 -m "Release v2.6.1"
git push origin v2.6.1
```

## Verify Release Assets and Checksums

- Confirm the release exists and capture assets:

```bash
gh release view v2.6.1 --json name,tagName,assets
gh release download v2.6.1 --pattern "checksums-*.txt"
```

- Verify checksums:

```bash
sha256sum -c checksums-yolo-runner_linux_amd64.tar.gz.txt
```

## Smoke Install and CLI Check

```bash
export TAG=v2.6.1
curl -fsSL -o /tmp/yolo-runner-linux-amd64.tar.gz https://github.com/egv/yolo-runner/releases/download/${TAG}/yolo-runner_linux_amd64.tar.gz
tar -xzf /tmp/yolo-runner-linux-amd64.tar.gz -C /tmp
/tmp/yolo-task --version
/tmp/yolo-tui --version
```
