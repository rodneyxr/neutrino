uds { bundle_api_version = "uds.dev/v1alpha1" }
metadata {
  name = "observatory"
  version = "1.0.0"
}
package "api" {
  source = "../package"
  values_files = ["values.yaml", "pending.yaml"]
}
package "worker" {
  source = "../package"
  depends_on = [package.api]
}
