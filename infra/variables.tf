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

variable "db_password" {
  description = "PostgreSQL Database Password"
  type        = string
  sensitive   = true
}

variable "db_name" {
  description = "PostgreSQL Database Name"
  type        = string
  default     = "antimoney"
}
