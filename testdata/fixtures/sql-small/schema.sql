CREATE TABLE users (
    id INT,
    name TEXT
);

CREATE TABLE orders (
    id INT,
    user_id INT
);

CREATE VIEW user_orders AS
SELECT u.name, o.id
FROM users u
JOIN orders o ON o.user_id = u.id;
