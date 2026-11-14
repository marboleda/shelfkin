locals {
  # The API's login role. The migrate job creates it with SQL and sets its
  # password from api-database-url; it is not a Cloud SQL user (see
  # database.tf and https://github.com/marboleda/shelfkin/issues/92).
  api_db_role = "shelfkin_api"

  # A DATABASE_URL that reaches the instance through the Cloud SQL Unix socket
  # that Cloud Run mounts at /cloudsql/<connection name>. pgx (#3) reads the
  # socket directory from the host parameter.
  database_urls = {
    api      = "postgres://${local.api_db_role}:${random_password.api.result}@/${local.database_name}?host=${local.cloudsql_mount}/${google_sql_database_instance.main.connection_name}"
    migrator = "postgres://${google_sql_user.migrator.name}:${random_password.migrator.result}@/${local.database_name}?host=${local.cloudsql_mount}/${google_sql_database_instance.main.connection_name}"
  }
}

# api-database-url is the single source of the shelfkin_api password: the API
# and the expiry job connect with it, and the migrate job reads it as
# API_DATABASE_URL to create shelfkin_api or set its password (#92).
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
