locals {
  # Bundle identifier (iOS) and package name (Android), shared with #33 and #41.
  app_id = "com.marboleda.shelfkin"

  hosting_url      = "https://${var.hosting_site_id}.web.app"
  hosting_domains  = ["${var.hosting_site_id}.web.app", "${var.hosting_site_id}.firebaseapp.com"]
  cors_origins     = join(",", [for d in local.hosting_domains : "https://${d}"])
  database_name    = "shelfkin"
  cloudsql_mount   = "/cloudsql"
  placeholder_api  = "us-docker.pkg.dev/cloudrun/container/hello"
  placeholder_job  = "us-docker.pkg.dev/cloudrun/container/job"
  repository_id    = "shelfkin"
  api_service_name = "shelfkin-api"
  migrate_job_name = "shelfkin-migrate"
}

data "google_project" "this" {
  project_id = var.project_id
}

resource "google_project_service" "this" {
  for_each = toset([
    "apikeys.googleapis.com",
    "artifactregistry.googleapis.com",
    "billingbudgets.googleapis.com",
    "cloudbilling.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "firebase.googleapis.com",
    "firebasehosting.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
    "identitytoolkit.googleapis.com",
    "run.googleapis.com",
    "secretmanager.googleapis.com",
    "serviceusage.googleapis.com",
    "sqladmin.googleapis.com",
    "sts.googleapis.com",
  ])

  project = var.project_id
  service = each.value

  # Leave APIs on during destroy: turning them off can fail on dependants and
  # gains nothing for an empty project.
  disable_on_destroy = false
}
