// Command fdt-keygen generates the trust-chain key pairs and optional device CA.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	"fdt/internal/crypto"
)

func main() {
	out := flag.String("out", "keys", "output directory for PEM keys")
	deviceCA := flag.Bool("device-ca", false, "also generate an intranet device CA")
	deviceID := flag.String("device", "", "issue a client cert for this ATM id (requires -device-ca or existing ca)")
	serverCN := flag.String("server", "", "issue a server cert with this CN (requires CA)")
	serverDNS := flag.String("server-dns", "localhost", "comma-separated DNS SANs for -server")
	serverIP := flag.String("server-ip", "127.0.0.1", "comma-separated IP SANs for -server")
	flag.Parse()

	if err := os.MkdirAll(*out, 0o700); err != nil {
		log.Fatalf("mkdir: %v", err)
	}
	for _, name := range []struct{ kid, priv, pub string }{
		{"internal", "sk_internal.pem", "pk_internal.pem"},
		{"gateway", "sk_gateway.pem", "pk_gateway.pem"},
	} {
		kp, err := crypto.GenerateKeyPair()
		if err != nil {
			log.Fatalf("keygen %s: %v", name.kid, err)
		}
		if err := crypto.WritePrivatePEM(filepath.Join(*out, name.priv), kp.Priv); err != nil {
			log.Fatal(err)
		}
		if err := crypto.WritePublicPEM(filepath.Join(*out, name.pub), kp.Pub); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("generated %s key pair -> %s, %s\n", name.kid,
			filepath.Join(*out, name.priv), filepath.Join(*out, name.pub))
	}

	caPath, caKeyPath := filepath.Join(*out, "device_ca.pem"), filepath.Join(*out, "device_ca.key")
	var ca *crypto.CA
	if *deviceCA {
		var err error
		ca, err = crypto.GenerateCA("FDT Device CA")
		if err != nil {
			log.Fatal(err)
		}
		if err := crypto.WriteCertPEM(caPath, ca.CertPEM, caKeyPath, ca.KeyPEM); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("generated device CA -> %s, %s\n", caPath, caKeyPath)
	} else if *deviceID != "" || *serverCN != "" {
		var err error
		ca, err = crypto.LoadCA(caPath, caKeyPath)
		if err != nil {
			log.Fatalf("load device CA (pass -device-ca first): %v", err)
		}
	}

	if *deviceID != "" {
		certPEM, keyPEM, fp, err := ca.IssueDevice(*deviceID)
		if err != nil {
			log.Fatal(err)
		}
		certOut := filepath.Join(*out, "device_"+*deviceID+".pem")
		keyOut := filepath.Join(*out, "device_"+*deviceID+".key")
		if err := crypto.WriteCertPEM(certOut, certPEM, keyOut, keyPEM); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("issued device cert %s fingerprint=%s\n", *deviceID, fp)
		fmt.Printf("  register with: -register-device %s -register-device-cert %s\n", *deviceID, certOut)
	}
	if *serverCN != "" {
		var ips []net.IP
		for _, s := range strings.Split(*serverIP, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if ip := net.ParseIP(s); ip != nil {
				ips = append(ips, ip)
			}
		}
		var dns []string
		for _, s := range strings.Split(*serverDNS, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				dns = append(dns, s)
			}
		}
		certPEM, keyPEM, err := ca.IssueServer(*serverCN, dns, ips)
		if err != nil {
			log.Fatal(err)
		}
		certOut := filepath.Join(*out, "server.pem")
		keyOut := filepath.Join(*out, "server.key")
		if err := crypto.WriteCertPEM(certOut, certPEM, keyOut, keyPEM); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("issued server cert -> %s, %s\n", certOut, keyOut)
	}

	fmt.Println("\n分发：PK_内网 → 网关；PK_网关 → 每台 ATM；device_ca.pem → datasvc/importer；device_*.pem/key → 对应 ATM。")
	fmt.Println("生产环境 SK_网关 应在 HSM 内生成且不可导出。")
}
