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

  depends_on = [google_firebase_project.this]
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
