output "api_url" {
  description = "URL of the shelfkin-api Cloud Run service."
  value       = google_cloud_run_v2_service.api.uri
}

output "hosting_url" {
  description = "URL of the Firebase Hosting site."
  value       = local.hosting_url
}

output "cloudsql_connection_name" {
  description = "Cloud SQL connection name (project:region:instance)."
  value       = google_sql_database_instance.main.connection_name
}

output "artifact_registry_repository" {
  description = "Docker image path prefix of the shelfkin repository, e.g. <this>/api:<tag>."
  value       = "${google_artifact_registry_repository.shelfkin.location}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.shelfkin.repository_id}"
}

output "workload_identity_provider" {
  description = "Full resource name of the GitHub OIDC provider, for google-github-actions/auth."
  value       = google_iam_workload_identity_pool_provider.github.name
}

output "deployer_service_account" {
  description = "Email of the github-deployer service account."
  value       = google_service_account.github_deployer.email
}

output "firebase_web_config" {
  description = "Firebase Web app config (#39, #41). The API key is not a secret: it only identifies the project."
  value = {
    apiKey     = data.google_firebase_web_app_config.web.api_key
    authDomain = data.google_firebase_web_app_config.web.auth_domain
    projectId  = var.project_id
  }
}

output "firebase_android_app_id" {
  description = "Firebase Android app ID (com.marboleda.shelfkin), for downloading google-services.json (#41)."
  value       = google_firebase_android_app.android.app_id
}

output "firebase_apple_app_id" {
  description = "Firebase Apple app ID (com.marboleda.shelfkin), for downloading GoogleService-Info.plist (#41)."
  value       = google_firebase_apple_app.ios.app_id
}

output "region" {
  description = "Region of every regional resource."
  value       = var.region
}
