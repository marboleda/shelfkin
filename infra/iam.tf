# Service accounts and their roles. No service account keys are created
# anywhere in this config: Cloud Run uses the attached runtime accounts and
# GitHub Actions uses Workload Identity Federation (wif.tf).

resource "google_service_account" "api_runtime" {
  account_id   = "api-runtime"
  display_name = "Cloud Run runtime for shelfkin-api"

  depends_on = [google_project_service.this]
}

resource "google_service_account" "migrate_runtime" {
  account_id   = "migrate-runtime"
  display_name = "Cloud Run runtime for shelfkin-migrate"

  depends_on = [google_project_service.this]
}

resource "google_service_account" "github_deployer" {
  account_id   = "github-deployer"
  display_name = "GitHub Actions deployer (main branch only)"

  depends_on = [google_project_service.this]
}

# --- api-runtime: connect to Cloud SQL, read its own DATABASE_URL ---

resource "google_project_iam_member" "api_runtime_sql_client" {
  project = var.project_id
  role    = "roles/cloudsql.client"
  member  = google_service_account.api_runtime.member
}

resource "google_secret_manager_secret_iam_member" "api_runtime" {
  secret_id = google_secret_manager_secret.api_database_url.id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.api_runtime.member
}

# --- migrate-runtime: connect to Cloud SQL, read its own DATABASE_URL ---

resource "google_project_iam_member" "migrate_runtime_sql_client" {
  project = var.project_id
  role    = "roles/cloudsql.client"
  member  = google_service_account.migrate_runtime.member
}

resource "google_secret_manager_secret_iam_member" "migrate_runtime" {
  secret_id = google_secret_manager_secret.migrator_database_url.id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.migrate_runtime.member
}

# --- github-deployer ---

# Push images to the shelfkin repository only.
resource "google_artifact_registry_repository_iam_member" "deployer_writer" {
  location   = google_artifact_registry_repository.shelfkin.location
  repository = google_artifact_registry_repository.shelfkin.name
  role       = "roles/artifactregistry.writer"
  member     = google_service_account.github_deployer.member
}

# Deploy shelfkin-api and update and run Cloud Run jobs. Granted on the
# project because `gcloud run deploy` and `gcloud run jobs execute --wait`
# also read operations and executions, which are not per-resource. Cloud Run
# Developer cannot change IAM policies.
resource "google_project_iam_member" "deployer_run_developer" {
  project = var.project_id
  role    = "roles/run.developer"
  member  = google_service_account.github_deployer.member
}

# Deploy Firebase Hosting (#39).
resource "google_project_iam_member" "deployer_hosting_admin" {
  project = var.project_id
  role    = "roles/firebasehosting.admin"
  member  = google_service_account.github_deployer.member
}

# Lets the deployer's API calls be billed to the project, which the Firebase
# CLI needs with federated credentials.
resource "google_project_iam_member" "deployer_service_usage" {
  project = var.project_id
  role    = "roles/serviceusage.serviceUsageConsumer"
  member  = google_service_account.github_deployer.member
}

# Act as the runtime accounts when deploying, on those accounts only (not
# project-wide).
resource "google_service_account_iam_member" "deployer_acts_as_api_runtime" {
  service_account_id = google_service_account.api_runtime.name
  role               = "roles/iam.serviceAccountUser"
  member             = google_service_account.github_deployer.member
}

resource "google_service_account_iam_member" "deployer_acts_as_migrate_runtime" {
  service_account_id = google_service_account.migrate_runtime.name
  role               = "roles/iam.serviceAccountUser"
  member             = google_service_account.github_deployer.member
}

# Enabling some APIs can create the Compute Engine default service account
# with Editor on the project. Make sure it does not keep that role, so Owner
# and Editor are held only by the human admin(s).
resource "google_project_iam_member_remove" "default_compute_editor" {
  project = var.project_id
  role    = "roles/editor"
  member  = "serviceAccount:${data.google_project.this.number}-compute@developer.gserviceaccount.com"

  depends_on = [google_project_service.this]
}
