# Watch Deploy

Poll a GitHub Actions deploy workflow until the Deploy step begins, then check cluster state.

## Usage

```
/watch-deploy
```

## Procedure

1. Find the latest "Deploy main" workflow run for the current branch.
2. Launch a background script that polls `gh run view` every 15s and exits when the "Deploy main" job step reaches `in_progress` or a terminal state.
3. When notified the script exited, read its output to determine state.
4. If deploy is running or completed, check `kubectl get pods -n mclaude-system` and report pod status.
5. If deploy failed, read the failed step logs via `gh api` and report the error.
6. If all pods are Running/Ready, report success. If any are crashing, read their logs and report.

## Background script template

```bash
#!/bin/bash
# Poll until the Deploy step starts or the run completes
RUN_ID="$1"
while true; do
  STATUS=$(gh run view "$RUN_ID" --json status,conclusion,jobs --jq '
    .jobs[] | select(.name=="Deploy main") | 
    if .conclusion != null then .conclusion
    elif (.steps // [] | map(select(.name | test("Helm deploy"))) | length > 0) then "deploying"  
    else "waiting"
    end
  ' 2>/dev/null | head -1)
  
  if [ "$STATUS" = "deploying" ] || [ "$STATUS" = "success" ] || [ "$STATUS" = "failure" ]; then
    echo "DEPLOY_STATUS=$STATUS"
    echo "RUN_ID=$RUN_ID"
    exit 0
  fi
  
  # If the whole run already finished
  OVERALL=$(gh run view "$RUN_ID" --json status --jq '.status' 2>/dev/null)
  if [ "$OVERALL" = "completed" ]; then
    CONCLUSION=$(gh run view "$RUN_ID" --json conclusion --jq '.conclusion' 2>/dev/null)
    echo "DEPLOY_STATUS=$CONCLUSION"
    echo "RUN_ID=$RUN_ID"
    exit 0
  fi
  
  sleep 15
done
```

Launch with `fireAndForget=true`, then wait for the completion notification. When notified, check cluster pods and CI status.
