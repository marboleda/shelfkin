resource "google_artifact_registry_repository" "shelfkin" {
  repository_id = local.repository_id
  location      = var.region
  format        = "DOCKER"
  description   = "Shelfkin container images"

  cleanup_policy_dry_run = false

  # KEEP wins over DELETE, so this deletes every version except the 20 most
  # recent.
  cleanup_policies {
    id     = "keep-20-most-recent"
    action = "KEEP"

    most_recent_versions {
      keep_count = 20
    }
  }

  cleanup_policies {
    id     = "delete-older"
    action = "DELETE"

    condition {
      tag_state = "ANY"
    }
  }

  depends_on = [google_project_service.this]
}
