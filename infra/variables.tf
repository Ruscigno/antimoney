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
# Set real values in terraform.tfvars (not committed with secrets in git history).
variable "plaid_client_id" {
  description = "Plaid client id (Trial/Production)"
  type        = string
  default     = ""
}

variable "plaid_secret" {
  description = "Plaid API secret"
  type        = string
  sensitive   = true
  default     = ""
}

variable "plaid_env" {
  description = "Plaid environment: sandbox or production"
  type        = string
  default     = "production"
}

variable "plaid_token_enc_key" {
  description = "Comma-separated base64 32-byte keys for access-token encryption at rest (first key encrypts; all keys decrypt — rotation path)"
  type        = string
  sensitive   = true
  default     = ""
}
