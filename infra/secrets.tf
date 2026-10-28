locals {
  # A DATABASE_URL that reaches the instance through the Cloud SQL Unix socket
  # that Cloud Run mounts at /cloudsql/<connection name>. pgx (#3) reads the
  # socket directory from the host parameter.
  database_urls = {
    api      = "postgres://${google_sql_user.api.name}:${random_password.api.result}@/${local.database_name}?host=${local.cloudsql_mount}/${google_sql_database_instance.main.connection_name}"
    migrator = "postgres://${google_sql_user.migrator.name}:${random_password.migrator.result}@/${local.database_name}?host=${local.cloudsql_mount}/${google_sql_database_instance.main.connection_name}"
  }
}

resource "google_secret_manager_secret" "api_database_url" {
  secret_id = "api-database-url"

  replication {
    user_managed {
      replicas {
        location = var.region
      }
    }
  }

  depends_on = [google_project_service.this]
}

resource "google_secret_manager_secret_version" "api_database_url" {
  secret      = google_secret_manager_secret.api_database_url.id
  secret_data = local.database_urls.api
}

resource "google_secret_manager_secret" "migrator_database_url" {
  secret_id = "migrator-database-url"

  replication {
    user_managed {
      replicas {
        location = var.region
      }
    }
  }

  depends_on = [google_project_service.this]
}

resource "google_secret_manager_secret_version" "migrator_database_url" {
  secret      = google_secret_manager_secret.migrator_database_url.id
  secret_data = local.database_urls.migrator
}
