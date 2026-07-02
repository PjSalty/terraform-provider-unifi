# List DPI application categories, e.g. to resolve the "Social Network" category id for a DPI grouping rule.
data "unifi_dpi_application_categories" "all" {}

output "dpi_category_ids" {
  value = { for c in data.unifi_dpi_application_categories.all.dpi_application_categories : c.name => c.id }
}
