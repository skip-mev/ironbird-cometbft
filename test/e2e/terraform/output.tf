output "cc_ipv4_address" {
  value = digitalocean_droplet.cc.ipv4_address
}

output "grafana_url" {
  value = "http://${digitalocean_droplet.cc.ipv4_address}:3000"
}

output "elastic_url" {
  value = "http://${digitalocean_droplet.cc.ipv4_address}:5601"
}
