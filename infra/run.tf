# Both Cloud Run resources start from a public placeholder image. #38 deploys
# the real image; Terraform ignores image changes so the next apply does not
# roll a deploy back. The client fields that gcloud sets on deploy are ignored
# for the same reason.

resource "google_cloud_run_v2_service" "api" {
  name     = local.api_service_name
  location = var.region
  ingress  = "INGRESS_TRAFFIC_ALL"

  # Anyone can invoke the service: the API authenticates users with Firebase
  # ID tokens itself, so a request reaches the container instead of IAM's 403.
  invoker_iam_disabled = true

  # Stateless, so it can be destroyed with the rest of the project.
  deletion_protection = false

  template {
    service_account = google_service_account.api_runtime.email

    scaling {
      min_instance_count = 0
      max_instance_count = 3
    }

    volumes {
      name = "cloudsql"

      cloud_sql_instance {
        instances = [google_sql_database_instance.main.connection_name]
      }
    }

    containers {
      image = local.placeholder_api

      ports {
        container_port = 8080
      }

      env {
        name = "DATABASE_URL"

        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.api_database_url.secret_id
            version = "latest"
          }
        }
      }

      env {
        name  = "FIREBASE_PROJECT_ID"
        value = var.project_id
      }

      env {
        name  = "APP_BASE_URL"
        value = local.hosting_url
      }

      env {
        name  = "CORS_ALLOWED_ORIGINS"
        value = local.cors_origins
      }

      env {
        name  = "LOG_LEVEL"
        value = "info"
      }

      # FIREBASE_AUTH_EMULATOR_HOST is deliberately never set: in production
      # the API must verify real Firebase ID tokens.

      volume_mounts {
        name       = "cloudsql"
        mount_path = local.cloudsql_mount
      }

      startup_probe {
        http_get {
          path = "/health"
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [
      template[0].containers[0].image,
      client,
      client_version,
    ]
  }

  depends_on = [
    google_project_service.this,
    google_secret_manager_secret_version.api_database_url,
    google_secret_manager_secret_iam_member.api_runtime,
    google_project_iam_member.api_runtime_sql_client,
  ]
}

resource "google_cloud_run_v2_job" "migrate" {
  name     = local.migrate_job_name
  location = var.region

  deletion_protection = false

  template {
    template {
      service_account = google_service_account.migrate_runtime.email
      max_retries     = 0

      volumes {
        name = "cloudsql"

        cloud_sql_instance {
          instances = [google_sql_database_instance.main.connection_name]
        }
      }

      containers {
        image = local.placeholder_job

        env {
          name = "DATABASE_URL"

          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.migrator_database_url.secret_id
              version = "latest"
            }
          }
        }

        volume_mounts {
          name       = "cloudsql"
          mount_path = local.cloudsql_mount
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [
      template[0].template[0].containers[0].image,
      client,
      client_version,
    ]
  }

  depends_on = [
    google_project_service.this,
    google_secret_manager_secret_version.migrator_database_url,
    google_secret_manager_secret_iam_member.migrate_runtime,
    google_project_iam_member.migrate_runtime_sql_client,
  ]
}
