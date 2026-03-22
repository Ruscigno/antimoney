output "db_internal_ip" {
  description = "The internal IP address of the Database VM"
  value       = google_compute_instance.db_instance.network_interface.0.network_ip
}

output "backend_url" {
  description = "The public URL of the Backend Cloud Run service"
  value       = google_cloud_run_v2_service.backend.uri
}

output "frontend_url" {
  description = "The public URL of the Frontend Cloud Run service (the actual app!)"
  value       = google_cloud_run_v2_service.frontend.uri
}

output "artifact_registry" {
  description = "The URL of the Artifact Registry repository"
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.repo.repository_id}"
}
