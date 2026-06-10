#!/bin/bash
#
# Deploy Antimoney to Google Cloud.
#
# The script is structured as functions so it can be sourced and unit-tested
# (see tests/deploy.bats). When executed directly it runs main().

# Strict mode for the whole script, so every helper function runs under the same
# errexit/pipefail guarantees whether it is invoked via main() or on its own.
set -eo pipefail

# Load variables from .env into the environment, if present.
# Uses `set -a` + `source` rather than `export $(... | xargs)` so values with
# spaces/quotes are handled correctly (xargs word-splits them — ShellCheck SC2046).
load_env() {
  if [ -f ".env" ]; then
    set -a
    # shellcheck source=/dev/null
    . ./.env
    set +a
  fi
}

# Validate PROJECT_ID and derive the deploy's configuration.
#
# CLOUDSDK_CORE_PROJECT is the SINGLE source of truth for project targeting:
# every gcloud command in this script inherits it, regardless of the user's
# currently-active gcloud config (which may point at an unrelated project).
# We deliberately do NOT also pass --project flags — one mechanism, not two.
require_project() {
  if [ -z "${PROJECT_ID:-}" ]; then
    echo "Error: PROJECT_ID environment variable is not set."
    echo "Usage: PROJECT_ID=my-project-id ./deploy.sh or add it to your .env file"
    return 1
  fi

  export CLOUDSDK_CORE_PROJECT="$PROJECT_ID"
  # Local to the script process — not exported, since child processes receive
  # what they need as explicit arguments.
  REGION="us-central1"
  ZONE="${REGION}-a"
  REPO_URL="${REGION}-docker.pkg.dev/${PROJECT_ID}/antimoney-repo"
}

apply_infra() {
  echo "[1/5] Applying Terraform Infrastructure..."
  ( cd infra && terraform init && terraform apply -var="project_id=${PROJECT_ID}" -auto-approve )
}

read_outputs() {
  BACKEND_URL=$(cd infra && terraform output -raw backend_url)
  FRONTEND_URL=$(cd infra && terraform output -raw frontend_url)
  STAGING_BUCKET=$(cd infra && terraform output -raw build_staging_bucket)
}

# Wait for the DB VM's startup script (Docker install + Postgres container) to
# finish. The VM has no public IP, so Postgres is unreachable from here; instead
# we poll the VM's serial console for the GCE startup-script completion marker.
# Returns as soon as the marker appears (unlike a blind sleep). Bounded by a
# timeout; if the marker never appears (e.g. the serial buffer rotated on a
# long-running VM) we warn and proceed, matching the previous best-effort wait.
wait_for_db() {
  # ZONE is derived by require_project; calling this in isolation without it
  # would silently poll the wrong (empty) zone forever.
  if [ -z "${ZONE:-}" ]; then
    echo "wait_for_db: ZONE is not set (call require_project first)" >&2
    return 1
  fi

  local timeout="${DB_WAIT_TIMEOUT:-120}"
  local interval="${DB_WAIT_INTERVAL:-10}"
  local marker="Finished running startup scripts"
  local elapsed=0
  # Guard against a misconfigured interval of 0 (or less), which would never
  # advance `elapsed` and loop forever.
  [ "$interval" -ge 1 ] || interval=1

  # Capture gcloud's stderr so a real failure (bad zone, missing
  # compute.instances.getSerialPortOutput permission, instance not created) is
  # surfaced on timeout instead of being silently swallowed by 2>/dev/null.
  local errfile
  errfile=$(mktemp)

  echo "[2/5] Waiting up to ${timeout}s for the DB VM startup script to finish..."
  while [ "$elapsed" -lt "$timeout" ]; do
    if gcloud compute instances get-serial-port-output antimoney-db \
        --zone="$ZONE" 2>"$errfile" | grep -q "$marker"; then
      rm -f "$errfile"
      echo "DB VM startup script finished."
      return 0
    fi
    sleep "$interval"
    elapsed=$(( elapsed + interval ))
  done

  echo "WARNING: DB readiness marker not seen after ${timeout}s; proceeding anyway."
  if [ -s "$errfile" ]; then
    echo "Last error from 'gcloud compute instances get-serial-port-output':" >&2
    cat "$errfile" >&2
  fi
  rm -f "$errfile"
  return 0
}

# Remove the DB VM's ephemeral public IP (only needed during boot to pull
# packages) to keep the VM on the Always Free tier. Idempotent: a no-op if the
# IP was already removed.
remove_db_public_ip() {
  echo "[3/5] Removing Public IP from Database VM to avoid the monthly charge..."
  local access_config
  access_config=$(gcloud compute instances describe antimoney-db \
    --zone="$ZONE" \
    --format="value(networkInterfaces[0].accessConfigs[0].name)")

  if [ -n "$access_config" ]; then
    gcloud compute instances delete-access-config antimoney-db \
      --zone="$ZONE" \
      --access-config-name="$access_config" \
      --quiet
    echo "Removed Public IP ($access_config). Database is now fully private."
  else
    echo "No Public IP found on the Database VM (perhaps already removed?)."
  fi
}

deploy_backend() {
  echo "[4/5] Building and Deploying Backend to Cloud Run..."
  gcloud builds submit --tag "${REPO_URL}/backend:latest" backend/ \
    --gcs-source-staging-dir "gs://${STAGING_BUCKET}/backend"

  gcloud run deploy antimoney-backend \
    --image "${REPO_URL}/backend:latest" \
    --region "$REGION" \
    --quiet
}

# Restore the Dockerfiles swapped by deploy_frontend, in the current directory.
# Idempotent: only acts while a swap is in progress (Dockerfile.dev present),
# so it is safe to run from both the EXIT and the INT/TERM traps.
restore_dockerfiles() {
  [ -f Dockerfile.dev ] || return 0
  mv -f Dockerfile Dockerfile.prod
  mv -f Dockerfile.dev Dockerfile
}

deploy_frontend() {
  echo "[5/5] Building and Deploying Frontend to Cloud Run..."
  # `gcloud builds submit --tag` builds the "Dockerfile" in the context root, but
  # the repo's frontend/Dockerfile is the dev image. Temporarily put the prod one
  # in its place. A trap restores the originals on ANY exit — success, error, or
  # Ctrl+C — so an interrupted build never leaves the workspace half-swapped.
  (
    cd frontend || exit 1
    # Preserve the failing exit status: a plain `trap restore EXIT` would let
    # restore_dockerfiles' own success (0) mask a failed build. Capture $? and
    # re-exit it so the subshell reflects the build result.
    # shellcheck disable=SC2154  # rc is assigned in the same trap action
    trap 'rc=$?; restore_dockerfiles; exit "$rc"' EXIT
    trap 'restore_dockerfiles; exit 130' INT TERM

    mv Dockerfile Dockerfile.dev
    mv Dockerfile.prod Dockerfile
    gcloud builds submit --tag "${REPO_URL}/frontend:latest" . \
      --gcs-source-staging-dir "gs://${STAGING_BUCKET}/frontend"
  ) || return 1  # build failed (Dockerfiles already restored by trap) — do not deploy

  gcloud run deploy antimoney-frontend \
    --image "${REPO_URL}/frontend:latest" \
    --region "$REGION" \
    --quiet
}

main() {
  echo "============================================="
  echo " Deploying Antimoney to Google Cloud"
  echo "============================================="

  load_env
  require_project
  apply_infra
  read_outputs
  wait_for_db
  remove_db_public_ip
  deploy_backend
  deploy_frontend

  echo "============================================="
  echo " 🎉 Deployment Complete! 🎉"
  echo "============================================="
  echo " Backend URL: $BACKEND_URL"
  echo " Frontend URL (Your App!): $FRONTEND_URL"
  echo "============================================="
}

# Run main only when executed directly, not when sourced by the test suite.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  main "$@"
fi
