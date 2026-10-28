# Offline tests: every provider is mocked, so `terraform test` needs no
# credentials and creates nothing. They check what the config asks GCP and
# Firebase for, not what GCP does with it.

mock_provider "google" {
  mock_data "google_project" {
    defaults = {
      number = "123456789012"
    }
  }

  mock_resource "google_sql_database_instance" {
    defaults = {
      connection_name = "test-project:europe-west1:shelfkin"
    }
  }

  mock_resource "google_service_account" {
    defaults = {
      email  = "mock-account@test-project.iam.gserviceaccount.com"
      member = "serviceAccount:mock-account@test-project.iam.gserviceaccount.com"
      name   = "projects/test-project/serviceAccounts/mock-account@test-project.iam.gserviceaccount.com"
    }
  }

  mock_resource "google_iam_workload_identity_pool" {
    defaults = {
      name = "projects/123456789012/locations/global/workloadIdentityPools/github"
    }
  }
}

mock_provider "google-beta" {
  mock_data "google_firebase_web_app_config" {
    defaults = {
      api_key     = "test-api-key"
      auth_domain = "test-project.firebaseapp.com"
    }
  }
}

mock_provider "random" {
  mock_resource "random_password" {
    defaults = {
      result = "Passw0rdWithNoSpecials"
    }
  }
}

variables {
  project_id        = "test-project"
  region            = "europe-west1"
  billing_account   = "012345-6789AB-CDEF01"
  hosting_site_id   = "shelfkin-test"
  budget_amount     = 30
  github_repository = "marboleda/shelfkin"
}

run "regional_resources_use_region" {
  command = plan

  assert {
    condition     = google_sql_database_instance.main.region == var.region
    error_message = "Cloud SQL must be in var.region"
  }

  assert {
    condition     = google_cloud_run_v2_service.api.location == var.region && google_cloud_run_v2_job.migrate.location == var.region
    error_message = "Cloud Run must be in var.region"
  }

  assert {
    condition     = google_artifact_registry_repository.shelfkin.location == var.region
    error_message = "Artifact Registry must be in var.region"
  }

  assert {
    condition = alltrue([
      google_secret_manager_secret.api_database_url.replication[0].user_managed[0].replicas[0].location == var.region,
      google_secret_manager_secret.migrator_database_url.replication[0].user_managed[0].replicas[0].location == var.region,
    ])
    error_message = "Secrets must be replicated in var.region only"
  }
}

run "database" {
  command = plan

  assert {
    condition     = google_sql_database_instance.main.database_version == "POSTGRES_18"
    error_message = "Cloud SQL must run PostgreSQL 18"
  }

  assert {
    condition     = google_sql_database_instance.main.settings[0].tier == "db-f1-micro"
    error_message = "Cloud SQL must use the smallest shared-core tier"
  }

  assert {
    condition     = google_sql_database_instance.main.deletion_protection && google_sql_database_instance.main.settings[0].deletion_protection_enabled
    error_message = "Deletion protection must be on in Terraform and in Cloud SQL"
  }

  assert {
    condition = alltrue([
      google_sql_database_instance.main.settings[0].backup_configuration[0].enabled,
      google_sql_database_instance.main.settings[0].backup_configuration[0].point_in_time_recovery_enabled,
    ])
    error_message = "Daily backups and point-in-time recovery must be on"
  }

  assert {
    condition     = length(google_sql_database_instance.main.settings[0].ip_configuration[0].authorized_networks) == 0
    error_message = "Cloud SQL must have no authorized networks"
  }

  assert {
    condition     = google_sql_database_instance.main.settings[0].ip_configuration[0].ssl_mode == "ENCRYPTED_ONLY"
    error_message = "Cloud SQL must only accept encrypted connections"
  }

  assert {
    condition     = google_sql_database.shelfkin.name == "shelfkin"
    error_message = "The database must be called shelfkin"
  }

  assert {
    condition = alltrue([
      google_sql_user.migrator.name == "shelfkin_migrator",
      google_sql_user.api.name == "shelfkin_api",
      google_sql_user.migrator.type == "BUILT_IN",
      google_sql_user.api.type == "BUILT_IN",
    ])
    error_message = "Expected built-in users shelfkin_migrator and shelfkin_api"
  }
}

run "database_urls_in_secret_manager" {
  assert {
    condition     = google_secret_manager_secret.api_database_url.secret_id == "api-database-url" && google_secret_manager_secret.migrator_database_url.secret_id == "migrator-database-url"
    error_message = "Unexpected secret IDs"
  }

  assert {
    condition     = google_secret_manager_secret_version.api_database_url.secret_data == "postgres://shelfkin_api:Passw0rdWithNoSpecials@/shelfkin?host=/cloudsql/test-project:europe-west1:shelfkin"
    error_message = "api-database-url must be a DATABASE_URL for the Cloud SQL Unix socket"
  }

  assert {
    condition     = google_secret_manager_secret_version.migrator_database_url.secret_data == "postgres://shelfkin_migrator:Passw0rdWithNoSpecials@/shelfkin?host=/cloudsql/test-project:europe-west1:shelfkin"
    error_message = "migrator-database-url must be a DATABASE_URL for the Cloud SQL Unix socket"
  }

  assert {
    condition     = google_sql_user.api.password == random_password.api.result && google_sql_user.migrator.password == random_password.migrator.result
    error_message = "User passwords must come from random_password"
  }
}

run "artifact_registry" {
  command = plan

  assert {
    condition     = google_artifact_registry_repository.shelfkin.repository_id == "shelfkin" && google_artifact_registry_repository.shelfkin.format == "DOCKER"
    error_message = "Expected a Docker repository called shelfkin"
  }

  assert {
    condition = anytrue([
      for p in google_artifact_registry_repository.shelfkin.cleanup_policies :
      p.action == "KEEP" && p.most_recent_versions[0].keep_count == 20
    ])
    error_message = "The cleanup policy must keep the 20 most recent images"
  }
}

run "cloud_run_service" {
  command = plan

  assert {
    condition     = google_cloud_run_v2_service.api.name == "shelfkin-api"
    error_message = "Service must be called shelfkin-api"
  }

  assert {
    condition = (
      google_cloud_run_v2_service.api.template[0].scaling[0].min_instance_count == 0 &&
      google_cloud_run_v2_service.api.template[0].scaling[0].max_instance_count == 3
    )
    error_message = "Service must scale from 0 to 3 instances"
  }

  assert {
    condition     = google_cloud_run_v2_service.api.invoker_iam_disabled
    error_message = "Service must be publicly invokable"
  }

  assert {
    condition     = google_cloud_run_v2_service.api.template[0].containers[0].startup_probe[0].http_get[0].path == "/health"
    error_message = "Service needs a startup probe on /health"
  }

  assert {
    condition = {
      for e in google_cloud_run_v2_service.api.template[0].containers[0].env : e.name => e.value if e.name != "DATABASE_URL"
      } == {
      FIREBASE_PROJECT_ID  = "test-project"
      APP_BASE_URL         = "https://shelfkin-test.web.app"
      CORS_ALLOWED_ORIGINS = "https://shelfkin-test.web.app,https://shelfkin-test.firebaseapp.com"
      LOG_LEVEL            = "info"
    }
    error_message = "Unexpected plain env vars (FIREBASE_AUTH_EMULATOR_HOST must never be set)"
  }

  assert {
    condition = one([
      for e in google_cloud_run_v2_service.api.template[0].containers[0].env :
      e.value_source[0].secret_key_ref[0].secret if e.name == "DATABASE_URL"
    ]) == "api-database-url"
    error_message = "DATABASE_URL must come from api-database-url"
  }

  assert {
    condition = (
      (length(google_cloud_run_v2_service.api.template[0].volumes[0].cloud_sql_instance[0].instances) == 1 && contains(google_cloud_run_v2_service.api.template[0].volumes[0].cloud_sql_instance[0].instances, "test-project:europe-west1:shelfkin")) &&
      google_cloud_run_v2_service.api.template[0].containers[0].volume_mounts[0].mount_path == "/cloudsql"
    )
    error_message = "Service must mount the Cloud SQL connection at /cloudsql"
  }
}

run "cloud_run_job" {
  command = plan

  assert {
    condition     = google_cloud_run_v2_job.migrate.name == "shelfkin-migrate"
    error_message = "Job must be called shelfkin-migrate"
  }

  assert {
    condition     = one([for e in google_cloud_run_v2_job.migrate.template[0].template[0].containers[0].env : e.value_source[0].secret_key_ref[0].secret if e.name == "DATABASE_URL"]) == "migrator-database-url"
    error_message = "Job DATABASE_URL must come from migrator-database-url"
  }

  assert {
    condition     = (length(google_cloud_run_v2_job.migrate.template[0].template[0].volumes[0].cloud_sql_instance[0].instances) == 1 && contains(google_cloud_run_v2_job.migrate.template[0].template[0].volumes[0].cloud_sql_instance[0].instances, "test-project:europe-west1:shelfkin"))
    error_message = "Job must have the Cloud SQL connection"
  }
}

run "service_accounts" {
  command = plan

  assert {
    condition = (
      google_service_account.api_runtime.account_id == "api-runtime" &&
      google_service_account.migrate_runtime.account_id == "migrate-runtime" &&
      google_service_account.github_deployer.account_id == "github-deployer"
    )
    error_message = "Unexpected service account IDs"
  }

  assert {
    condition     = google_secret_manager_secret_iam_member.api_runtime.role == "roles/secretmanager.secretAccessor"
    error_message = "api-runtime must be Secret Accessor on its secret"
  }

  assert {
    condition = alltrue([
      for m in [
        google_project_iam_member.api_runtime_sql_client,
        google_project_iam_member.migrate_runtime_sql_client,
        google_project_iam_member.deployer_run_developer,
        google_project_iam_member.deployer_hosting_admin,
        google_project_iam_member.deployer_service_usage,
      ] : !contains(["roles/owner", "roles/editor"], m.role)
    ])
    error_message = "No service account may get Owner or Editor"
  }

  assert {
    condition = (
      google_service_account_iam_member.deployer_acts_as_api_runtime.role == "roles/iam.serviceAccountUser" &&
      google_service_account_iam_member.deployer_acts_as_migrate_runtime.role == "roles/iam.serviceAccountUser"
    )
    error_message = "Deployer must be Service Account User on the runtime accounts"
  }

  assert {
    condition     = google_project_iam_member_remove.default_compute_editor.member == "serviceAccount:123456789012-compute@developer.gserviceaccount.com"
    error_message = "Editor must be removed from the default compute service account"
  }
}

run "workload_identity" {
  command = plan

  assert {
    condition     = google_iam_workload_identity_pool_provider.github.attribute_condition == "assertion.repository == 'marboleda/shelfkin' && assertion.ref == 'refs/heads/main'"
    error_message = "Only marboleda/shelfkin on refs/heads/main may get a token"
  }

  assert {
    condition     = google_iam_workload_identity_pool_provider.github.oidc[0].issuer_uri == "https://token.actions.githubusercontent.com"
    error_message = "Provider must trust GitHub's OIDC issuer"
  }

  assert {
    condition     = google_service_account_iam_member.github_impersonates_deployer.role == "roles/iam.workloadIdentityUser"
    error_message = "The pool must be allowed to impersonate github-deployer"
  }
}

run "firebase" {
  command = plan

  assert {
    condition = (
      google_identity_platform_config.this.sign_in[0].email[0].enabled &&
      !google_identity_platform_config.this.sign_in[0].anonymous[0].enabled &&
      !google_identity_platform_config.this.sign_in[0].phone_number[0].enabled
    )
    error_message = "Email/password must be the only sign-in provider"
  }

  assert {
    condition     = toset(google_identity_platform_config.this.authorized_domains) == toset(["shelfkin-test.web.app", "shelfkin-test.firebaseapp.com", "localhost"])
    error_message = "Authorized domains must be the two Hosting domains and localhost"
  }

  assert {
    condition     = google_firebase_hosting_site.web.site_id == "shelfkin-test"
    error_message = "Hosting site must use hosting_site_id"
  }

  assert {
    condition     = google_firebase_android_app.android.package_name == "com.marboleda.shelfkin" && google_firebase_apple_app.ios.bundle_id == "com.marboleda.shelfkin"
    error_message = "Mobile apps must use com.marboleda.shelfkin"
  }
}

run "budget" {
  command = plan

  assert {
    condition     = [for r in google_billing_budget.project.threshold_rules : r.threshold_percent] == [0.5, 0.9, 1.0]
    error_message = "Budget must alert at 50%, 90% and 100%"
  }

  assert {
    condition     = google_billing_budget.project.amount[0].specified_amount[0].units == "30"
    error_message = "Budget amount must come from budget_amount"
  }

  assert {
    condition     = google_billing_budget.project.budget_filter[0].projects == toset(["projects/123456789012"])
    error_message = "Budget must be scoped to this project"
  }
}

run "outputs" {
  assert {
    condition     = output.hosting_url == "https://shelfkin-test.web.app"
    error_message = "Unexpected Hosting URL"
  }

  assert {
    condition     = output.artifact_registry_repository == "europe-west1-docker.pkg.dev/test-project/shelfkin"
    error_message = "Unexpected Artifact Registry path"
  }

  assert {
    condition     = output.cloudsql_connection_name == "test-project:europe-west1:shelfkin"
    error_message = "Unexpected connection name"
  }

  assert {
    condition = output.firebase_web_config == {
      apiKey     = "test-api-key"
      authDomain = "test-project.firebaseapp.com"
      projectId  = "test-project"
    }
    error_message = "Unexpected Firebase web config"
  }
}

run "hosting_site_id_must_differ_from_project_id" {
  command = plan

  variables {
    hosting_site_id = "test-project"
  }

  expect_failures = [var.hosting_site_id]
}
