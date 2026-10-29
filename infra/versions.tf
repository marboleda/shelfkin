terraform {
  # Everyone applies with the same Terraform. CI uses this version too.
  required_version = "= 1.14.3"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "= 8.8.1"
    }
    google-beta = {
      source  = "hashicorp/google-beta"
      version = "= 8.8.1"
    }
    random = {
      source  = "hashicorp/random"
      version = "= 3.9.1"
    }
  }

  # State lives in a GCS bucket created once by hand (see "Infrastructure" in
  # the README). The bucket name is passed at init time:
  #   terraform init -backend-config="bucket=<project_id>-tfstate"
  backend "gcs" {
    prefix = "terraform/state"
  }
}

# Both providers bill API calls to the project itself instead of the
# credentials' own quota project. The Billing Budgets and Firebase APIs reject
# user credentials without this.
provider "google" {
  project               = var.project_id
  region                = var.region
  user_project_override = true
  billing_project       = var.project_id
}

provider "google-beta" {
  project               = var.project_id
  region                = var.region
  user_project_override = true
  billing_project       = var.project_id
}
