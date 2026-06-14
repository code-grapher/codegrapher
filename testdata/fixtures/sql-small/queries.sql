-- Standalone query referencing a table defined in schema.sql (cross-file link).
SELECT id, name FROM users WHERE id = 1;

-- Cross-file join: users and orders are both created in schema.sql.
SELECT u.name, o.id
FROM users u
JOIN orders o ON o.user_id = u.id;
