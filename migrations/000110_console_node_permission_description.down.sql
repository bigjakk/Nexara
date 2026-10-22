UPDATE permissions
SET description = 'Open node shell consoles (root shell on the Proxmox host)'
WHERE action = 'console' AND resource = 'node';
