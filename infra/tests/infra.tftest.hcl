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

  # terraform_data.email_privacy PATCHes the real Identity Toolkit API from a
  # local-exec provisioner. Mocked providers and override_resource do not stop
  # a provisioner, and the run blocks with the default `command = apply` would
  # run it. So it is off here: no network call, no gcloud or curl needed. Only
  # `command = plan` runs (which never run provisioners) may turn it on.
  email_privacy_patch = false
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
      google_sql_user.migrator.type == "BUILT_IN",
    ])
    error_message = "Expected built-in user shelfkin_migrator (shelfkin_api is created by the migrate job, #92)"
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
    condition     = google_secret_manager_secret_version.api_database_url.secret_data == "postgres://shelfkin_api:${random_password.api.result}@/shelfkin?host=/cloudsql/test-project:europe-west1:shelfkin"
    error_message = "api-database-url must use random_password.api"
  }

  assert {
    condition     = google_sql_user.migrator.password == random_password.migrator.result
    error_message = "The shelfkin_migrator password must come from random_password"
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
    condition = [
      for e in google_cloud_run_v2_job.migrate.template[0].template[0].containers[0].env :
      [e.value_source[0].secret_key_ref[0].secret, e.value_source[0].secret_key_ref[0].version] if e.name == "API_DATABASE_URL"
    ] == [["api-database-url", "latest"]]
    error_message = "Job API_DATABASE_URL must come from the latest api-database-url"
  }

  assert {
    condition     = (length(google_cloud_run_v2_job.migrate.template[0].template[0].volumes[0].cloud_sql_instance[0].instances) == 1 && contains(google_cloud_run_v2_job.migrate.template[0].template[0].volumes[0].cloud_sql_instance[0].instances, "test-project:europe-west1:shelfkin"))
    error_message = "Job must have the Cloud SQL connection"
  }
}

run "service_accounts" {
  # An apply (all mocked, email_privacy_patch off) so the secret IDs compared
  # below are known.
  command = apply

  # The mock gives every service account the same member; tell the runtime
  # accounts apart so "only" can be checked. A state of its own, so the
  # overrides are used instead of the accounts in earlier runs' state.
  state_key = "service_accounts"

  override_resource {
    target = google_service_account.api_runtime
    values = {
      email  = "api-runtime@test-project.iam.gserviceaccount.com"
      member = "serviceAccount:api-runtime@test-project.iam.gserviceaccount.com"
      name   = "projects/test-project/serviceAccounts/api-runtime@test-project.iam.gserviceaccount.com"
    }
  }

  override_resource {
    target = google_service_account.migrate_runtime
    values = {
      email  = "migrate-runtime@test-project.iam.gserviceaccount.com"
      member = "serviceAccount:migrate-runtime@test-project.iam.gserviceaccount.com"
      name   = "projects/test-project/serviceAccounts/migrate-runtime@test-project.iam.gserviceaccount.com"
    }
  }

  assert {
    condition = (
      google_service_account.api_runtime.account_id == "api-runtime" &&
      google_service_account.migrate_runtime.account_id == "migrate-runtime" &&
      google_service_account.github_deployer.account_id == "github-deployer"
    )
    error_message = "Unexpected service account IDs"
  }

  assert {
    condition = (
      google_secret_manager_secret_iam_member.api_runtime.role == "roles/secretmanager.secretAccessor" &&
      google_secret_manager_secret_iam_member.api_runtime.secret_id == google_secret_manager_secret.api_database_url.id &&
      google_secret_manager_secret_iam_member.api_runtime.member == google_service_account.api_runtime.member
    )
    error_message = "api-runtime must be Secret Accessor on api-database-url"
  }

  assert {
    condition = alltrue([
      for m in [
        google_secret_manager_secret_iam_member.migrate_runtime,
        google_secret_manager_secret_iam_member.migrate_runtime_api_url,
      ] : m.role == "roles/secretmanager.secretAccessor" && m.member == google_service_account.migrate_runtime.member
      ]) && toset([
      google_secret_manager_secret_iam_member.migrate_runtime.secret_id,
      google_secret_manager_secret_iam_member.migrate_runtime_api_url.secret_id,
    ]) == toset([google_secret_manager_secret.migrator_database_url.id, google_secret_manager_secret.api_database_url.id])
    error_message = "migrate-runtime must be Secret Accessor on migrator-database-url and api-database-url"
  }

  assert {
    condition = alltrue([
      for m in [
        google_secret_manager_secret_iam_member.migrate_runtime,
        google_secret_manager_secret_iam_member.migrate_runtime_api_url,
      ] : m.member != google_service_account.api_runtime.member
    ])
    error_message = "api-runtime must have access to api-database-url only"
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
    condition     = google_iam_workload_identity_pool_provider.github.attribute_condition == "assertion.repository == 'marboleda/shelfkin' && assertion.ref == 'refs/heads/main' && assertion.event_name in ['workflow_run', 'workflow_dispatch']"
    error_message = "Only workflow_run and workflow_dispatch runs of marboleda/shelfkin on refs/heads/main may get a token"
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
  # Must stay a plan: with email_privacy_patch on, an apply would run the PATCH.
  command = plan

  variables {
    email_privacy_patch = true
  }

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

  assert {
    condition     = google_identity_platform_config.this.monitoring[0].request_logging[0].enabled
    error_message = "Identity Platform request logging must be on, so disabled-account sign-ins are logged server-side"
  }

  assert {
    condition = (
      terraform_data.email_privacy[0].input.project_id == var.project_id &&
      terraform_data.email_privacy[0].input.enable_improved_email_privacy == true &&
      terraform_data.email_privacy[0].triggers_replace == terraform_data.email_privacy[0].input
    )
    error_message = "terraform_data.email_privacy must turn on enable_improved_email_privacy for var.project_id, and re-run when that changes"
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
    condition     = output.project_id == "test-project" && output.region == "europe-west1" && output.hosting_site_id == "shelfkin-test"
    error_message = "Unexpected project, region or Hosting site outputs"
  }

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
