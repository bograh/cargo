-- name: GetIdentityByIssuerSubject :one
SELECT * FROM auth_identities WHERE issuer = $1 AND subject = $2;

-- name: CreateIdentity :one
INSERT INTO auth_identities (user_id, issuer, subject, email)
VALUES ($1, $2, $3, $4)
RETURNING *;
