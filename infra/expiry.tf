# The expiry notification job (#22) in production (#40): a Cloud Run job that
# Cloud Scheduler starts at minute 0 of every hour, and the alerts that email
# var.alert_email when it fails or stops running. See "Expiry job in
# production" in the README.

locals {
  expiry_job_name = "shelfkin-expiry-job"

  # Cloud Scheduler is not offered in every Cloud Run region.
  scheduler_region = coalesce(var.scheduler_region, var.region)
}

resource "google_project_service" "expiry" {
  for_each = toset([
    "cloudscheduler.googleapis.com",
    "monitoring.googleapis.com",
  ])

  project            = var.project_id
  service            = each.value
  disable_on_destroy = false
}

# --- Service accounts ---

resource "google_service_account" "expiry_job_runtime" {
  account_id   = "expiry-job-runtime"
  display_name = "Cloud Run runtime for shelfkin-expiry-job"

  depends_on = [google_project_service.this]
}

resource "google_service_account" "scheduler_invoker" {
  account_id   = "scheduler-invoker"
  display_name = "Cloud Scheduler: runs shelfkin-expiry-job only"

  depends_on = [google_project_service.this]
}

# expiry-job-runtime: connect to Cloud SQL, and read api-database-url (it logs
# in as shelfkin_api, like the API) and expo-access-token. Nothing else.
resource "google_project_iam_member" "expiry_job_runtime_sql_client" {
  project = var.project_id
  role    = "roles/cloudsql.client"
  member  = google_service_account.expiry_job_runtime.member
}

resource "google_secret_manager_secret_iam_member" "expiry_job_runtime_database_url" {
  secret_id = google_secret_manager_secret.api_database_url.id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.expiry_job_runtime.member
}

resource "google_secret_manager_secret_iam_member" "expiry_job_runtime_expo_token" {
  secret_id = google_secret_manager_secret.expo_access_token.id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.expiry_job_runtime.member
}

# scheduler-invoker: run this one job, granted on the job (not the project).
# Cloud Run Invoker allows a plain run but not a run with overrides.
resource "google_cloud_run_v2_job_iam_member" "scheduler_invoker" {
  name     = google_cloud_run_v2_job.expiry.name
  location = google_cloud_run_v2_job.expiry.location
  role     = "roles/run.invoker"
  member   = google_service_account.scheduler_invoker.member
}

# The deploy workflow (#38) points the job at each new image; updating a job
# needs Service Account User on its runtime account (this account only).
resource "google_service_account_iam_member" "deployer_acts_as_expiry_job_runtime" {
  service_account_id = google_service_account.expiry_job_runtime.name
  role               = "roles/iam.serviceAccountUser"
  member             = google_service_account.github_deployer.member
}

# --- Expo access token ---

# Only needed when Expo's enhanced push security is on. Terraform creates the
# secret but never a version, so the token is never in code or state: it is
# added by hand (README) and the job reads it only once
# var.expo_access_token_enabled is true (a secret with no version would stop
# the job from starting).
resource "google_secret_manager_secret" "expo_access_token" {
  secret_id = "expo-access-token"

  replication {
    user_managed {
      replicas {
        location = var.region
      }
    }
  }

  depends_on = [google_project_service.this]
}

# --- The job ---

resource "google_cloud_run_v2_job" "expiry" {
  name     = local.expiry_job_name
  location = var.region

  deletion_protection = false

  template {
    task_count = 1

    template {
      service_account = google_service_account.expiry_job_runtime.email
      timeout         = "900s"
      max_retries     = 1

      volumes {
        name = "cloudsql"

        cloud_sql_instance {
          instances = [google_sql_database_instance.main.connection_name]
        }
      }

      containers {
        image = local.placeholder_job

        # The image has no ENTRYPOINT and its default command is the API
        # server (#38). No args here, so `--args=--dry-run` on an execution
        # becomes `expiry-job --dry-run`.
        command = ["/usr/local/bin/expiry-job"]

        # shelfkin_api, which works through the #10 helper as shelfkin_app
        # (#22). Never the migrator's URL.
        env {
          name = "DATABASE_URL"

          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.api_database_url.secret_id
              version = "latest"
            }
          }
        }

        dynamic "env" {
          for_each = var.expo_access_token_enabled ? [1] : []

          content {
            name = "EXPO_ACCESS_TOKEN"

            value_source {
              secret_key_ref {
                secret  = google_secret_manager_secret.expo_access_token.secret_id
                version = "latest"
              }
            }
          }
        }

        env {
          name  = "LOG_LEVEL"
          value = "info"
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
    google_secret_manager_secret_version.api_database_url,
    google_secret_manager_secret_iam_member.expiry_job_runtime_database_url,
    google_secret_manager_secret_iam_member.expiry_job_runtime_expo_token,
    google_project_iam_member.expiry_job_runtime_sql_client,
  ]
}

# --- The schedule ---

resource "google_cloud_scheduler_job" "expiry" {
  name        = local.expiry_job_name
  description = "Runs the Cloud Run job ${local.expiry_job_name} at minute 0 of every hour (#40)."
  region      = local.scheduler_region
  schedule    = "0 * * * *"
  time_zone   = "Etc/UTC"

  http_target {
    http_method = "POST"
    uri         = "https://run.googleapis.com/v2/projects/${var.project_id}/locations/${google_cloud_run_v2_job.expiry.location}/jobs/${google_cloud_run_v2_job.expiry.name}:run"

    oauth_token {
      service_account_email = google_service_account.scheduler_invoker.email
      scope                 = "https://www.googleapis.com/auth/cloud-platform"
    }
  }

  # Pausing and resuming with gcloud (README) is not drift.
  lifecycle {
    ignore_changes = [paused]
  }

  depends_on = [
    google_project_service.expiry,
    google_cloud_run_v2_job_iam_member.scheduler_invoker,
  ]
}

# --- Alerting ---

resource "google_monitoring_notification_channel" "alert_email" {
  display_name = "Shelfkin alerts (${var.alert_email})"
  type         = "email"

  labels = {
    email_address = var.alert_email
  }

  depends_on = [google_project_service.expiry]
}

locals {
  expiry_execution_filter = join(" AND ", [
    "resource.type = \"cloud_run_job\"",
    "resource.labels.job_name = \"${local.expiry_job_name}\"",
    "metric.type = \"run.googleapis.com/job/completed_execution_count\"",
  ])
}

# An execution only fails once its task has failed on the retry too.
resource "google_monitoring_alert_policy" "expiry_job_failed" {
  display_name = "${local.expiry_job_name}: execution failed"
  combiner     = "OR"

  conditions {
    display_name = "A ${local.expiry_job_name} execution failed after its retry"

    condition_threshold {
      filter          = "${local.expiry_execution_filter} AND metric.labels.result = \"failed\""
      comparison      = "COMPARISON_GT"
      threshold_value = 0
      duration        = "0s"

      aggregations {
        alignment_period     = "60s"
        per_series_aligner   = "ALIGN_SUM"
        cross_series_reducer = "REDUCE_SUM"
      }

      trigger {
        count = 1
      }
    }
  }

  alert_strategy {
    auto_close = "3600s"
  }

  notification_channels = [google_monitoring_notification_channel.alert_email.id]

  documentation {
    mime_type = "text/markdown"
    content   = "An execution of the Cloud Run job `${local.expiry_job_name}` failed after its retry. Check the execution's logs (Cloud Run > Jobs > ${local.expiry_job_name} > Executions) and see \"Expiry job in production\" in the README, including how to replay a missed hour with `--now`."
  }
}

# Catches a paused, deleted or broken schedule as well as repeated failures.
resource "google_monitoring_alert_policy" "expiry_job_not_succeeding" {
  display_name = "${local.expiry_job_name}: no successful run for 3 hours"
  combiner     = "OR"

  conditions {
    display_name = "No ${local.expiry_job_name} execution has succeeded for 3 hours"

    condition_absent {
      filter   = "${local.expiry_execution_filter} AND metric.labels.result = \"succeeded\""
      duration = "10800s"

      aggregations {
        alignment_period     = "300s"
        per_series_aligner   = "ALIGN_SUM"
        cross_series_reducer = "REDUCE_SUM"
      }

      trigger {
        count = 1
      }
    }
  }

  notification_channels = [google_monitoring_notification_channel.alert_email.id]

  documentation {
    mime_type = "text/markdown"
    content   = "No execution of the Cloud Run job `${local.expiry_job_name}` has succeeded for 3 hours. Check that the Cloud Scheduler job `${local.expiry_job_name}` is enabled and its last attempt succeeded, and look at recent executions. See \"Expiry job in production\" in the README."
  }
}
