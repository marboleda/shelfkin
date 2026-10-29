# GitHub Actions authenticates with its OIDC token, so no key is stored in
# GitHub. Only workflows in var.github_repository running on refs/heads/main
# get a token: other branches, pull requests (refs/pull/...) and forks (a
# different repository claim) are rejected by the attribute condition.
#
# The condition also admits only the events the deploy workflows use
# (workflow_run after CI succeeds on main, and workflow_dispatch; #38, #39).
# That rejects pull_request_target, which runs on refs/heads/main in this
# repository even when a fork opens the pull request.

resource "google_iam_workload_identity_pool" "github" {
  workload_identity_pool_id = "github"
  display_name              = "GitHub Actions"

  depends_on = [google_project_service.this]
}

resource "google_iam_workload_identity_pool_provider" "github" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "github-actions"
  display_name                       = "GitHub Actions OIDC"

  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "attribute.repository" = "assertion.repository"
    "attribute.ref"        = "assertion.ref"
  }

  attribute_condition = join(" && ", [
    "assertion.repository == '${var.github_repository}'",
    "assertion.ref == 'refs/heads/main'",
    "assertion.event_name in ['workflow_run', 'workflow_dispatch']",
  ])

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
}

resource "google_service_account_iam_member" "github_impersonates_deployer" {
  service_account_id = google_service_account.github_deployer.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/attribute.repository/${var.github_repository}"
}
