# While the droplets are being created, compile and compress app binary.
resource "terraform_data" "app-binary" {
  provisioner "local-exec" {
    # command     = "go build -o build/app ./node && tar -cvzf build/app.tgz --directory build app"
    command     = "go build -o build/app ./node"
    working_dir = ".."
    environment = {
      "CGO_ENABLED" = "0",
      "GOOS"        = "linux",
      "GOARCH"      = "amd64",
    }
  }
}

# Upload compressed binary to CC.
resource "terraform_data" "binary-remote" {
  triggers_replace = [
    terraform_data.app-binary,
    digitalocean_droplet.cc.id
  ]

  connection {
    host        = digitalocean_droplet.cc.ipv4_address
    timeout     = var.ssh_timeout
    private_key = tls_private_key.ssh.private_key_openssh
  }

  provisioner "file" {
    source      = "../build/app"
    destination = "/root/app"
  }
}

# Once cloud-init is done on CC, set up NFS directory and uncompress app binary
# in it.
resource "terraform_data" "cc-nfs" {
  triggers_replace = [
    digitalocean_droplet.cc.id,
    # terraform_data.cc-done.id,
    terraform_data.binary-remote
  ]

  connection {
    host        = digitalocean_droplet.cc.ipv4_address
    timeout     = var.ssh_timeout
    private_key = tls_private_key.ssh.private_key_openssh
  }

  provisioner "remote-exec" {
    inline = [
      # Block until cloud-init completes.
      "cloud-init status --wait > /dev/null 2>&1",
      # Set up NFS.
      "mkdir /data",
      "chown nobody:nogroup /data",
      "systemctl start nfs-kernel-server",
      "systemctl enable nfs-kernel-server",
      # Now that the NFS directory is ready, put the app binary in it.
      "mv /root/app /data",
      "chmod +x /data/app",
      # "mv /root/app.tgz /data",
      # "cd /data && tar -xvzf app.tgz"
    ]
  }
}