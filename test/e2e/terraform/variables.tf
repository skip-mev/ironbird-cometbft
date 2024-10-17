variable "testnet_dir" {
  type = string
}

variable "vpc_subnet" {
  type = string
  # TODO: remove default
  default = "172.16.0.0/20"
  # default = "172.19.144.0/20"
}

variable "do_token" {}

variable "ssh_keys" {
  type = list(string)
}

variable "project_name" {
  type    = string
  default = "CometBFT"
}

variable "node_names" {
  type    = list(string)
  default = []
}

# Regions and number of servers to deploy there
# Regions list: https://docs.digitalocean.com/platform/regional-availability/
# ams3 - Amsterdam
# blr1 - Bangalore
# fra1 - Frankfurt
# lon1 - London
# nyc1 - New York City
# nyc3 - New York City
# sfo2 - San Francisco
# sfo3 - San Francisco
# sgp1 - Singapore
# syd1 - Sydney
# tor1 - Toronto
variable "region" {
  type    = string
  default = "ams3"
}

# Cheapest droplet size
variable "shared" {
  type    = string
  default = "s-4vcpu-8gb"
}

# Small droplet size
variable "small" {
  type    = string
  default = "g-2vcpu-8gb"
}

# Large droplet size
variable "large" {
  type    = string
  default = "g-4vcpu-16gb"
}

# Type of servers to deploy into each region
variable "cc_size" {
  type    = string
  default = "so-4vcpu-32gb-intel"
}

variable "tags" {
  type    = list(string)
  default = ["CometBFT"]
}

variable "ssh_timeout" {
  type    = string
  default = "60s"
}

variable "manifest_path" {
  type    = string
}

locals {
  do_project_name = lower("${var.project_name}-testnet")
  testnet_size    = length(var.node_names)
  nodes = [
    for node in digitalocean_droplet.node :
    {
      name        = node.name,
      urn         = node.urn,
      ip          = node.ipv4_address,
      internal_ip = node.ipv4_address_private
    }
  ]
  cc = {
    name        = digitalocean_droplet.cc.name
    ip          = digitalocean_droplet.cc.ipv4_address
    internal_ip = digitalocean_droplet.cc.ipv4_address_private
  }
}
