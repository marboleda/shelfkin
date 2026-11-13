# Firebase on the same GCP project. Accepting the Firebase terms cannot be
# done in Terraform; see the README.

resource "google_firebase_project" "this" {
  provider = google-beta
  project  = var.project_id

  depends_on = [google_project_service.this]
}

# Identity Platform (Firebase Auth) with email/password as the only provider.
resource "google_identity_platform_config" "this" {
  project = var.project_id

  sign_in {
    allow_duplicate_emails = false

    email {
      enabled           = true
      password_required = true
    }

    anonymous {
      enabled = false
    }

    phone_number {
      enabled = false
    }
  }

  # Exactly these three, and Terraform owns the list. Firebase adds its default
  # domains (<project_id>.firebaseapp.com and .web.app) when it is enabled;
  # this resource is applied after that and replaces them. If anything adds a
  # domain later (for example creating the default Hosting site from the
  # console), the next plan shows it and apply restores this list. It is not
  # in ignore_changes, because that would let the list drift silently from
  # "the two Hosting domains and localhost".
  authorized_domains = concat(local.hosting_domains, ["localhost"])

  # Log every Auth API request to Cloud Logging, including failed sign-ins
  # such as USER_DISABLED (the app shows those as a wrong password). See
  # "Auth request logs" in the README for the query, cost and PII notes.
  monitoring {
    request_logging {
      enabled = true
    }
  }

  depends_on = [google_firebase_project.this]
}

# Email enumeration protection ("improved email privacy").
#
# - Why not an argument above: google_identity_platform_config has no
#   email_privacy_config / enable_improved_email_privacy argument in the pinned
#   google and google-beta 8.8.1, nor in the latest release (8.11.0). So this
#   resource PATCHes the Identity Toolkit admin API directly.
# - Drift: the provisioner runs only when this resource is created or its
#   triggers_replace changes. Terraform does NOT read the setting back, so it
#   will not notice someone turning protection off in the console. The curl
#   check under "Email enumeration protection" in the README is how to catch
#   that.
# - Replace this with the native argument once the provider has one (then
#   `terraform state rm 'terraform_data.email_privacy[0]'`).
#
# Needs bash, curl and gcloud (logged in with `gcloud auth login`) on the
# machine that applies; see Prerequisites in the README.
#
# Declared here rather than in variables.tf because it only exists for this
# resource. Real applies leave it at true. `terraform test` sets it to false
# so the PATCH never runs there (mocked providers do not stop a provisioner,
# and neither does override_resource).
variable "email_privacy_patch" {
  description = "Create terraform_data.email_privacy, which turns on email enumeration protection with a PATCH to the Identity Toolkit API. Leave true; only the offline tests set it to false."
  type        = bool
  default     = true
  nullable    = false
}

locals {
  email_privacy = {
    project_id                    = var.project_id
    enable_improved_email_privacy = true
  }
}

resource "terraform_data" "email_privacy" {
  count = var.email_privacy_patch ? 1 : 0

  input            = local.email_privacy
  triggers_replace = local.email_privacy

  provisioner "local-exec" {
    interpreter = ["bash", "-c"]
    environment = {
      PROJECT_ID = self.input.project_id
      BODY       = jsonencode({ emailPrivacyConfig = { enableImprovedEmailPrivacy = self.input.enable_improved_email_privacy } })
    }
    # x-goog-user-project bills the call to the project, for the same reason
    # the providers set user_project_override.
    command = <<-EOT
      set -euo pipefail
      TOKEN="$(gcloud auth print-access-token)"
      curl --silent --show-error --fail-with-body -X PATCH \
        -H "Authorization: Bearer $TOKEN" \
        -H "x-goog-user-project: $PROJECT_ID" \
        -H "Content-Type: application/json" \
        --data "$BODY" \
        "https://identitytoolkit.googleapis.com/admin/v2/projects/$PROJECT_ID/config?updateMask=emailPrivacyConfig.enableImprovedEmailPrivacy"
      echo
    EOT
  }

  depends_on = [google_identity_platform_config.this]
}

resource "google_firebase_web_app" "web" {
  provider     = google-beta
  project      = var.project_id
  display_name = "Shelfkin Web"

  depends_on = [google_firebase_project.this]
}

data "google_firebase_web_app_config" "web" {
  provider   = google-beta
  project    = var.project_id
  web_app_id = google_firebase_web_app.web.app_id
}

resource "google_firebase_hosting_site" "web" {
  provider = google-beta
  project  = var.project_id
  site_id  = var.hosting_site_id
  app_id   = google_firebase_web_app.web.app_id
}

resource "google_firebase_android_app" "android" {
  provider     = google-beta
  project      = var.project_id
  display_name = "Shelfkin Android"
  package_name = local.app_id

  depends_on = [google_firebase_project.this]
}

resource "google_firebase_apple_app" "ios" {
  provider     = google-beta
  project      = var.project_id
  display_name = "Shelfkin iOS"
  bundle_id    = local.app_id

  depends_on = [google_firebase_project.this]
}
