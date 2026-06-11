terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
  zone    = var.zone
}

resource "random_password" "db_password" {
  length           = 16
  special          = true
  override_special = "_%@"
}

resource "random_password" "jwt_secret" {
  length  = 32
  special = false
}

# 1. Enable Required APIs (Cloud Run, Compute Engine, Artifact Registry)
resource "google_project_service" "apis" {
  for_each = toset([
    "compute.googleapis.com",
    "run.googleapis.com",
    "artifactregistry.googleapis.com",
    "cloudbuild.googleapis.com",
    "secretmanager.googleapis.com"
  ])
  service            = each.key
  disable_on_destroy = false
}

# Plaid secrets live in Secret Manager, not as plaintext env vars: env vars are
# readable by any principal with run.services.get and land verbatim in the
# Terraform state. Terraform manages only the secret CONTAINERS and IAM; the
# secret VALUES are added out-of-band so they never touch variables or state.
#
# BOOTSTRAP ORDER (Cloud Run validates `latest` at rollout, so wiring the env
# before a version exists fails the deploy — hence the two flags):
#   1. terraform apply -var enable_plaid=true            # creates containers + IAM only
#   2. printf '%s' "$PLAID_SECRET"        | gcloud secrets versions add plaid-secret        --data-file=-
#      printf '%s' "$PLAID_TOKEN_ENC_KEY" | gcloud secrets versions add plaid-token-enc-key --data-file=-
#   3. terraform apply -var enable_plaid=true -var plaid_secrets_ready=true   # wires the env
#
# (JWT_SECRET / DATABASE_URL predate this PR and keep their existing pattern.)
locals {
  plaid_secret_ids = var.enable_plaid ? toset(["plaid-secret", "plaid-token-enc-key"]) : toset([])
}

resource "google_secret_manager_secret" "plaid" {
  for_each  = local.plaid_secret_ids
  secret_id = each.key
  replication {
    auto {}
  }
  depends_on = [google_project_service.apis]
}

data "google_project" "current" {}

# Validates bootstrap step 2 actually happened, METADATA-ONLY: the provider's
# plural versions data source (no secret_data) requires google provider 6.x,
# and the singular one would copy secret_data into the state — the exact leak
# this design eliminates. gcloud (already required by deploy.sh) lists version
# names only; nothing sensitive enters the state.
data "external" "plaid_secret_has_version" {
  for_each = var.plaid_secrets_ready ? google_secret_manager_secret.plaid : {}
  program = ["bash", "-c", <<-EOT
    if gcloud secrets versions list ${each.value.secret_id} --project ${var.project_id} --filter='state=ENABLED' --format='value(name)' --limit=1 2>/dev/null | grep -q .; then
      echo '{"has_version":"true"}'
    else
      echo '{"has_version":"false"}'
    fi
  EOT
  ]
  depends_on = [google_secret_manager_secret.plaid]
}

# The backend's Cloud Run runtime SA must be able to read the secrets.
resource "google_secret_manager_secret_iam_member" "plaid_accessor" {
  for_each  = google_secret_manager_secret.plaid
  secret_id = each.value.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${data.google_project.current.number}-compute@developer.gserviceaccount.com"
}

# 2. Firewall rule to allow internal traffic to Postgres port 5432
resource "google_compute_firewall" "allow_postgres_internal" {
  name    = "allow-postgres-internal"
  network = "default"

  allow {
    protocol = "tcp"
    ports    = ["5432"]
  }

  # Allow traffic from within the VPC (including Cloud Run Direct VPC Egress)
  source_ranges = ["10.0.0.0/8"]
  target_tags   = ["postgres-db"]

  depends_on = [google_project_service.apis]
}

# 3. Database VM (Always Free Tier eligible: e2-micro, us-central1, standard disk)
resource "google_compute_instance" "db_instance" {
  name         = "antimoney-db"
  machine_type = "e2-micro"
  zone         = var.zone

  boot_disk {
    initialize_params {
      image = "ubuntu-os-cloud/ubuntu-2204-lts"
      size  = 30
      type  = "pd-standard"
    }
  }

  network_interface {
    network = "default"
    # Ephemeral external IP required to download packages during startup
    access_config {}
  }

  tags = ["postgres-db"]

  # Startup script to install Docker and run Postgres
  metadata_startup_script = <<-EOT
    #!/bin/bash
    apt-get update
    apt-get install -y docker.io
    systemctl enable docker
    systemctl start docker
    
    docker run -d \
      --name postgres \
      --restart always \
      -e POSTGRES_USER=${var.db_user} \
      -e POSTGRES_PASSWORD=${random_password.db_password.result} \
      -e POSTGRES_DB=${var.db_name} \
      -p 5432:5432 \
      -v /opt/postgres_data:/var/lib/postgresql/data \
      postgres:16-alpine
  EOT

  lifecycle {
    ignore_changes = [
      network_interface[0].access_config
    ]
  }

  depends_on = [google_project_service.apis]
}

# 4. Artifact Registry for our Docker images with cleanup policies
resource "google_artifact_registry_repository" "repo" {
  location      = var.region
  repository_id = "antimoney-repo"
  description   = "Docker repository for Antimoney images"
  format        = "DOCKER"

  cleanup_policies {
    id     = "delete-untagged"
    action = "DELETE"
    condition {
      tag_state = "UNTAGGED"
    }
  }

  cleanup_policies {
    id     = "keep-last-5"
    action = "KEEP"
    most_recent_versions {
      keep_count = 5
    }
  }

  depends_on = [google_project_service.apis]
}

# 5. Build Staging Bucket with TTL (Delete build source code after 7 days)
resource "google_storage_bucket" "build_staging" {
  name          = "${var.project_id}-build-staging"
  location      = var.region
  force_destroy = true

  # Disable soft delete to avoid costs for deleted objects
  soft_delete_policy {
    retention_duration_seconds = 0
  }

  lifecycle_rule {
    condition {
      age = 7 # Delete after 7 days
    }
    action {
      type = "Delete"
    }
  }

  depends_on = [google_project_service.apis]
}

# 5. Backend Cloud Run Service
# NOTE: Terraform initially deploys a placeholder image. The deploy.sh script updates it with the real built application.
resource "google_cloud_run_v2_service" "backend" {
  name     = "antimoney-backend"
  location = var.region
  ingress  = "INGRESS_TRAFFIC_ALL"

  template {
    containers {
      image = "us-docker.pkg.dev/cloudrun/container/hello" # Placeholder
      
      env {
        name  = "ENVIRONMENT"
        value = "production"
      }
      env {
        name  = "DATABASE_URL"
        value = "postgres://${var.db_user}:${random_password.db_password.result}@${google_compute_instance.db_instance.network_interface.0.network_ip}:5432/${var.db_name}?sslmode=disable"
      }
      env {
        name  = "JWT_SECRET"
        value = random_password.jwt_secret.result
      }
      env {
        name  = "REDIS_URL"
        value = var.redis_url
      }
      env {
        name  = "CORS_ALLOWED_ORIGINS"
        value = var.cors_allowed_origins
      }
      env {
        name  = "PLAID_CLIENT_ID"
        value = var.plaid_client_id # an identifier, not a secret
      }
      env {
        name  = "PLAID_ENV"
        value = var.plaid_env
      }
      dynamic "env" {
        # Gated on plaid_secrets_ready: Cloud Run validates `latest` at rollout,
        # so the env must only be wired after the versions exist (bootstrap
        # step 3 — see the comment at the secret resources).
        for_each = var.plaid_secrets_ready ? google_secret_manager_secret.plaid : {}
        content {
          name = env.key == "plaid-secret" ? "PLAID_SECRET" : "PLAID_TOKEN_ENC_KEY"
          value_source {
            secret_key_ref {
              secret  = env.value.secret_id
              version = "latest"
            }
          }
        }
      }
    }

    # Connect Cloud Run to the default VPC so it can reach the DB VM's internal IP
    vpc_access {
      network_interfaces {
        network    = "default"
        subnetwork = "default"
      }
      egress = "PRIVATE_RANGES_ONLY"
    }
  }

  lifecycle {
    ignore_changes = [
      template[0].containers[0].image, # Ignore image changes so deployments outside TF don't get reverted
    ]
    # Bootstrap-order guards (round-12 #4): catch misuse at plan time instead
    # of failing the Cloud Run rollout.
    precondition {
      condition     = !var.plaid_secrets_ready || var.enable_plaid
      error_message = "plaid_secrets_ready=true requires enable_plaid=true (the secrets must exist before they can be wired)."
    }
    precondition {
      condition = alltrue([
        for k, v in data.external.plaid_secret_has_version : v.result.has_version == "true"
      ])
      error_message = "plaid_secrets_ready=true but a Plaid secret has no ENABLED version yet — run bootstrap step 2 (gcloud secrets versions add) first."
    }
  }

  # The IAM grant (and the secrets it references) must exist before a revision
  # that mounts them rolls out, or the deploy fails validation.
  depends_on = [google_project_service.apis, google_secret_manager_secret_iam_member.plaid_accessor]
}

resource "google_cloud_run_service_iam_member" "backend_public" {
  location = google_cloud_run_v2_service.backend.location
  service  = google_cloud_run_v2_service.backend.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# 6. Frontend Cloud Run Service
resource "google_cloud_run_v2_service" "frontend" {
  name     = "antimoney-frontend"
  location = var.region
  ingress  = "INGRESS_TRAFFIC_ALL"

  template {
    containers {
      image = "us-docker.pkg.dev/cloudrun/container/hello" # Placeholder
      
      env {
        name  = "BACKEND_URL"
        value = google_cloud_run_v2_service.backend.uri
      }
    }
  }

  lifecycle {
    ignore_changes = [
      template[0].containers[0].image,
    ]
  }

  depends_on = [google_project_service.apis]
}

resource "google_cloud_run_service_iam_member" "frontend_public" {
  location = google_cloud_run_v2_service.frontend.location
  service  = google_cloud_run_v2_service.frontend.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}
