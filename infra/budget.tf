# Emails the billing account's admins (the default IAM recipients) as spend
# for this project crosses each threshold.
resource "google_billing_budget" "project" {
  billing_account = var.billing_account
  display_name    = "${var.project_id} monthly"

  budget_filter {
    projects = ["projects/${data.google_project.this.number}"]
  }

  amount {
    # No currency_code: the budget uses the billing account's currency.
    specified_amount {
      units = tostring(var.budget_amount)
    }
  }

  threshold_rules {
    threshold_percent = 0.5
  }

  threshold_rules {
    threshold_percent = 0.9
  }

  threshold_rules {
    threshold_percent = 1.0
  }

  # No all_updates_rule: without one, alerts go to the default IAM recipients
  # (Billing Account Administrators and Users) by email.

  depends_on = [google_project_service.this]
}
