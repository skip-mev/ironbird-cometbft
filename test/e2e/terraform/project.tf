resource "digitalocean_project" "main-testnet" {
  name        = local.do_project_name
  description = "A project to test the ${var.project_name} codebase."
  resources = concat([
    for node in local.nodes :
    node.urn
  ], [digitalocean_droplet.cc.urn])
}

resource "digitalocean_vpc" "testnet-vpc" {
  name     = lower(replace("vpc-${var.project_name}-${var.vpc_subnet}", "/[/.]/", "-"))
  region   = var.region
  ip_range = var.vpc_subnet

  # workaround? https://github.com/digitalocean/terraform-provider-digitalocean/issues/488
  timeouts {
    delete = "30m"
  }
}
