Guided Soar server installation for Ubuntu 24.04 amd64. Includes explained
English terminal prompts, Persian instructions, DNS conflict checks, dedicated
systemd service, key-preserving updates/rollback and certificate-verified
HTTPS diagnostics. Generates complete IR/GLOBAL import links for WhiteDNS.

Install from an interactive SSH terminal:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/WhiteDNS/Soar-Server/main/setup.sh)
```

Requires a VPS, a domain and access to its DNS panel. Follow the records shown
by setup; existing Master/Cotten tunnel records should use separate subdomains.
See README and SERVER-GUIDE.fa.md. Iran field verification remains outstanding.
