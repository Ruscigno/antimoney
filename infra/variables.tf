variable "project_id" {
  description = "The Google Cloud Project ID"
  type        = string
}

variable "region" {
  description = "The Google Cloud region (must be us-central1, us-east1, or us-west1 for free tierVM)"
  type        = string
  default     = "us-central1"
}

variable "zone" {
  description = "The Google Cloud zone"
  type        = string
  default     = "us-central1-a"
}

variable "db_user" {
  description = "PostgreSQL Database Username"
  type        = string
  default     = "antimoney"
}

variable "db_name" {
  description = "PostgreSQL Database Name"
  type        = string
  default     = "antimoney"
}

variable "redis_url" {
  description = "Redis connection URL (e.g. rediss://... for TLS-enabled Upstash)"
  type        = string
  sensitive   = true
}

variable "cors_allowed_origins" {
  description = "Comma-separated list of allowed CORS origins for the backend API"
  type        = string
  default     = "https://superestruturas.com"
}

# Plaid bank sync (optional — the backend disables the feature when unset).
# enable_plaid creates the Secret Manager containers and wires the Cloud Run
# env; the secret VALUES are added out-of-band with `gcloud secrets versions
# add` (see infra/main.tf) so they never pass through variables or state.
variable "enable_plaid" {
  description = "Provision the Plaid Secret Manager containers and IAM (step 1 of the bootstrap)"
  type        = bool
  default     = false
}

variable "plaid_secrets_ready" {
  description = "Wire the Plaid secrets into the backend env. Set to true ONLY after adding the secret versions out-of-band (step 3) — Cloud Run validates 'latest' at rollout and a versionless secret fails the deploy."
  type        = bool
  default     = false
}

variable "plaid_client_id" {
  description = "Plaid client id (an identifier, not a secret)"
  type        = string
  default     = ""
}

variable "plaid_env" {
  description = "Plaid environment: sandbox or production"
  type        = string
  default     = "production"
}
