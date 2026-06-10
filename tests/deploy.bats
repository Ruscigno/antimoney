#!/usr/bin/env bats
#
# Unit tests for deploy.sh. gcloud/terraform are replaced with stubs on PATH so
# nothing touches real infrastructure; the stubs record how they were called.

setup() {
  REPO_ROOT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"

  # Stub directory placed first on PATH.
  STUB_BIN="$BATS_TEST_TMPDIR/bin"
  mkdir -p "$STUB_BIN"
  CALLS="$BATS_TEST_TMPDIR/gcloud_calls.log"
  : > "$CALLS"

  # gcloud stub: logs the inherited project + args, and emulates the handful of
  # subcommands the script depends on.
  cat > "$STUB_BIN/gcloud" <<EOF
#!/usr/bin/env bash
echo "project=\${CLOUDSDK_CORE_PROJECT:-UNSET} args=\$*" >> "$CALLS"
case "\$1 \$2 \$3" in
  "compute instances get-serial-port-output")
    if [ -n "\${STUB_SERIAL_ERR:-}" ]; then
      echo "ERROR: PERMISSION_DENIED getSerialPortOutput" >&2
      exit 1
    fi
    [ -n "\${STUB_NO_MARKER:-}" ] || echo "startup-script: ... Finished running startup scripts"
    ;;
  "compute instances describe")
    echo "\${STUB_ACCESS_CONFIG:-}"
    ;;
esac
exit 0
EOF
  chmod +x "$STUB_BIN/gcloud"

  # terraform stub: logs its args and returns canned values for `output -raw`.
  cat > "$STUB_BIN/terraform" <<EOF
#!/usr/bin/env bash
echo "terraform \$*" >> "$CALLS"
case "\$1" in
  output)
    case "\$3" in
      backend_url)          echo "https://backend.example" ;;
      frontend_url)         echo "https://frontend.example" ;;
      build_staging_bucket) echo "stage-bucket" ;;
    esac
    ;;
esac
exit 0
EOF
  chmod +x "$STUB_BIN/terraform"

  PATH="$STUB_BIN:$PATH"

  # Source the script: defines functions but does NOT run main (guarded).
  # shellcheck source=/dev/null
  source "$REPO_ROOT/deploy.sh"
}

@test "require_project exports CLOUDSDK_CORE_PROJECT from PROJECT_ID" {
  PROJECT_ID="my-proj"
  require_project
  [ "$CLOUDSDK_CORE_PROJECT" = "my-proj" ]
  # Confirm it is exported (visible to child processes).
  run bash -c 'echo "$CLOUDSDK_CORE_PROJECT"'
  [ "$output" = "my-proj" ]
}

@test "require_project derives region/zone/repo from PROJECT_ID" {
  PROJECT_ID="my-proj"
  require_project
  [ "$REGION" = "us-central1" ]
  [ "$ZONE" = "us-central1-a" ]
  [ "$REPO_URL" = "us-central1-docker.pkg.dev/my-proj/antimoney-repo" ]
}

@test "require_project fails when PROJECT_ID is unset" {
  unset PROJECT_ID
  run require_project
  [ "$status" -ne 0 ]
  [[ "$output" == *"PROJECT_ID"* ]]
}

@test "every gcloud call inherits the pinned project (no UNSET)" {
  PROJECT_ID="my-proj"
  require_project
  STAGING_BUCKET="bucket"
  run deploy_backend
  [ "$status" -eq 0 ]
  # builds submit + run deploy were both recorded with the right project.
  [ "$(grep -c 'project=my-proj' "$CALLS")" -ge 2 ]
  ! grep -q 'project=UNSET' "$CALLS"
}

@test "wait_for_db returns immediately when the startup marker is present" {
  PROJECT_ID="my-proj"; require_project
  DB_WAIT_INTERVAL=0
  run wait_for_db
  [ "$status" -eq 0 ]
  [[ "$output" == *"startup script finished"* ]]
}

@test "wait_for_db warns on timeout and never hangs (interval clamped, not 0)" {
  PROJECT_ID="my-proj"; require_project
  export STUB_NO_MARKER=1
  DB_WAIT_TIMEOUT=2
  DB_WAIT_INTERVAL=0   # would loop forever without the clamp in wait_for_db
  run wait_for_db
  [ "$status" -eq 0 ]
  [[ "$output" == *"WARNING"* ]]
}

@test "remove_db_public_ip deletes the access config when present" {
  PROJECT_ID="my-proj"; require_project
  export STUB_ACCESS_CONFIG="external-nat"
  run remove_db_public_ip
  [ "$status" -eq 0 ]
  grep -q 'delete-access-config' "$CALLS"
  [[ "$output" == *"Removed Public IP"* ]]
}

@test "remove_db_public_ip is a no-op when no public IP is attached" {
  PROJECT_ID="my-proj"; require_project
  export STUB_ACCESS_CONFIG=""
  run remove_db_public_ip
  [ "$status" -eq 0 ]
  ! grep -q 'delete-access-config' "$CALLS"
  [[ "$output" == *"No Public IP"* ]]
}

@test "restore_dockerfiles puts the swapped files back" {
  cd "$BATS_TEST_TMPDIR"
  echo "DEV"  > Dockerfile.dev   # mid-swap state created by deploy_frontend
  echo "PROD" > Dockerfile
  restore_dockerfiles
  [ "$(cat Dockerfile)" = "DEV" ]
  [ "$(cat Dockerfile.prod)" = "PROD" ]
  [ ! -f Dockerfile.dev ]
}

@test "restore_dockerfiles is a no-op (and safe to re-run) when not mid-swap" {
  cd "$BATS_TEST_TMPDIR"
  echo "DEV"  > Dockerfile
  echo "PROD" > Dockerfile.prod
  run restore_dockerfiles
  [ "$status" -eq 0 ]
  # Files untouched — guards against the double-restore overwrite bug.
  [ "$(cat Dockerfile)" = "DEV" ]
  [ "$(cat Dockerfile.prod)" = "PROD" ]
}

@test "load_env loads .env values, preserving spaces (the SC2046 fix)" {
  cd "$BATS_TEST_TMPDIR"
  cat > .env <<'ENVEOF'
# comment lines are ignored
PROJECT_ID=from-dotenv
LABEL="hello world"
ENVEOF
  load_env
  [ "$PROJECT_ID" = "from-dotenv" ]
  # A quoted value with a space survives `source`; the old
  # `export $(... | xargs)` would have word-split it into "hello".
  [ "$LABEL" = "hello world" ]
}

@test "load_env is a no-op (and succeeds) when .env is absent" {
  cd "$BATS_TEST_TMPDIR"
  run load_env
  [ "$status" -eq 0 ]
}

@test "wait_for_db surfaces the gcloud error on timeout instead of swallowing it" {
  PROJECT_ID="my-proj"; require_project
  export STUB_SERIAL_ERR=1
  DB_WAIT_TIMEOUT=1
  DB_WAIT_INTERVAL=0
  run wait_for_db
  [ "$status" -eq 0 ]
  [[ "$output" == *"WARNING"* ]]
  [[ "$output" == *"PERMISSION_DENIED"* ]]
}

@test "deploy_frontend swaps in the prod Dockerfile to build, then the trap restores the originals" {
  PROJECT_ID="my-proj"; require_project
  STAGING_BUCKET="bucket"
  cd "$BATS_TEST_TMPDIR"
  mkdir -p frontend
  echo "DEV"  > frontend/Dockerfile        # dev image (default)
  echo "PROD" > frontend/Dockerfile.prod   # prod image (built)

  run deploy_frontend
  [ "$status" -eq 0 ]

  # Trap reversed the swap: originals are back and no leftover Dockerfile.dev.
  [ "$(cat frontend/Dockerfile)" = "DEV" ]
  [ "$(cat frontend/Dockerfile.prod)" = "PROD" ]
  [ ! -f frontend/Dockerfile.dev ]

  # The prod image was built and the service deployed.
  grep -q 'builds submit' "$CALLS"
  grep -q 'run deploy antimoney-frontend' "$CALLS"
}

@test "apply_infra runs terraform with the project_id var" {
  PROJECT_ID="my-proj"; require_project
  cd "$BATS_TEST_TMPDIR"; mkdir -p infra
  run apply_infra
  [ "$status" -eq 0 ]
  grep -q 'terraform apply -var=project_id=my-proj' "$CALLS"
}

@test "read_outputs populates URLs and the staging bucket from terraform outputs" {
  cd "$BATS_TEST_TMPDIR"; mkdir -p infra
  read_outputs
  [ "$BACKEND_URL" = "https://backend.example" ]
  [ "$FRONTEND_URL" = "https://frontend.example" ]
  [ "$STAGING_BUCKET" = "stage-bucket" ]
}
