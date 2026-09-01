# amhVPN subscription harvester

`run.ps1` fetches the URLs in `sources.txt`, parses them with amhVPN's existing
parser, removes duplicate connection specifications, and checks up to 500 unique
nodes with isolated Mihomo measurers. A node is published only after two real
HTTP requests through its local proxy succeed.

Run a non-writing check:

```powershell
.\tools\sub-harvester\run.ps1 -DryRun
```

Run and publish when at least five nodes pass both checks:

```powershell
.\tools\sub-harvester\run.ps1
```

The script writes `worker/data/status.json` for every non-dry run. It preserves
the previous `healthy.txt` when fewer than five nodes are confirmed, and only
commits and pushes when a healthy list actually changed.

For later Windows Task Scheduler use, create a task that runs PowerShell with
`-File C:\WhiteVPN-Desktop-1.0.18\tools\sub-harvester\run.ps1`; do not schedule
it until a manual run has produced a healthy list.
