resource "google_sql_database_instance" "main" {
  name             = "shelfkin"
  region           = var.region
  database_version = "POSTGRES_18" # same major version as the local image (#3)

  # Terraform refuses to destroy the instance while this is true.
  deletion_protection = true

  settings {
    # Shared-core tiers are only offered on the Enterprise edition.
    edition           = "ENTERPRISE"
    tier              = "db-f1-micro"
    availability_type = "ZONAL"

    # Also blocks deletion through the console and gcloud.
    deletion_protection_enabled = true

    backup_configuration {
      enabled                        = true
      start_time                     = "03:00" # UTC
      point_in_time_recovery_enabled = true
      transaction_log_retention_days = 7

      backup_retention_settings {
        retained_backups = 7
        retention_unit   = "COUNT"
      }
    }

    ip_configuration {
      # A public IP with no authorized networks accepts no direct
      # connections: the only way in is a Cloud SQL connector, such as Cloud
      # Run's built-in Cloud SQL connection, which authenticates with IAM.
      ipv4_enabled = true
      ssl_mode     = "ENCRYPTED_ONLY"
    }
  }

  depends_on = [google_project_service.this]
}

resource "google_sql_database" "shelfkin" {
  name     = local.database_name
  instance = google_sql_database_instance.main.name
}

# Passwords are letters and digits only so they need no escaping in a URL.
resource "random_password" "migrator" {
  length  = 32
  special = false
}

# The shelfkin_api password. There is no Cloud SQL user for it: it only goes
# into api-database-url, and the migrate job sets it on the role (#92).
resource "random_password" "api" {
  length  = 32
  special = false
}

# Runs migrations (#38) and owns the schema.
resource "google_sql_user" "migrator" {
  name     = "shelfkin_migrator"
  instance = google_sql_database_instance.main.name
  type     = "BUILT_IN"
  password = random_password.migrator.result

  # Postgres refuses to drop a role that owns objects. On destroy the users go
  # with the instance anyway.
  deletion_policy = "ABANDON"
}

# shelfkin_api, the login the API and the expiry job use before switching to
# shelfkin_app (#10), is not a Cloud SQL user. Cloud SQL makes every built-in
# user a member of cloudsqlsuperuser, granted as cloudsqladmin, and no role we
# have can revoke that, so the #38 grant step rightly refuses such a login.
# Instead the migrate job, as shelfkin_migrator, creates shelfkin_api with SQL
# and keeps its password in step with api-database-url (api/cmd/migrate,
# https://github.com/marboleda/shelfkin/issues/92).
#
# The old built-in user is forgotten from state without a Cloud SQL API call;
# the README ("Moving shelfkin_api off the Cloud SQL built-in user") says how
# it is then deleted by hand, once.
removed {
  from = google_sql_user.api

  lifecycle {
    destroy = false
  }
}
