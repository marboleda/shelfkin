variable "project_id" {
  description = "ID of the existing GCP project that holds everything (Firebase is added to the same project)."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.project_id))
    error_message = "project_id must be a valid GCP project ID: 6-30 lowercase letters, digits or hyphens, starting with a letter."
  }
}

variable "region" {
  description = "The one region every regional resource is created in (Cloud SQL, Cloud Run, Artifact Registry, Secret Manager replicas)."
  type        = string
}

variable "billing_account" {
  description = "Billing account ID (XXXXXX-XXXXXX-XXXXXX) the project is linked to. Used for the budget."
  type        = string

  validation {
    condition     = can(regex("^[0-9A-F]{6}-[0-9A-F]{6}-[0-9A-F]{6}$", var.billing_account))
    error_message = "billing_account must look like 012345-6789AB-CDEF01."
  }
}

variable "hosting_site_id" {
  description = "Firebase Hosting site ID. The web app is served at https://<hosting_site_id>.web.app. Globally unique."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9-]{2,61}[a-z0-9]$", var.hosting_site_id))
    error_message = "hosting_site_id must be a valid domain label: lowercase letters, digits and hyphens."
  }

  # Firebase may already have created a default site named after the project,
  # which Terraform would then fail to create. Use a distinct site ID.
  validation {
    condition     = var.hosting_site_id != var.project_id
    error_message = "hosting_site_id must differ from project_id (that ID is reserved for Firebase's default site)."
  }
}

variable "budget_amount" {
  description = "Monthly budget in the billing account's currency. Billing admins are emailed at 50%, 90% and 100%."
  type        = number

  validation {
    condition     = var.budget_amount > 0 && floor(var.budget_amount) == var.budget_amount
    error_message = "budget_amount must be a positive whole number."
  }
}

variable "github_repository" {
  description = "GitHub repository (owner/name) whose main branch may deploy through Workload Identity Federation."
  type        = string

  validation {
    condition     = can(regex("^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$", var.github_repository))
    error_message = "github_repository must be owner/name, e.g. marboleda/shelfkin."
  }
}
