-- name: ListFirewallTemplates :many
SELECT * FROM firewall_templates ORDER BY name;

-- name: GetFirewallTemplate :one
SELECT * FROM firewall_templates WHERE id = $1;

-- name: CreateFirewallTemplate :one
INSERT INTO firewall_templates (name, description, rules)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateFirewallTemplate :one
UPDATE firewall_templates
SET name = $2, description = $3, rules = $4, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteFirewallTemplate :exec
DELETE FROM firewall_templates WHERE id = $1;
