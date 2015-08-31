CREATE SCHEMA jupiter_brain;

CREATE TABLE jupiter_brain.instances (
  id uuid UNIQUE NOT NULL,
  created_at timestamp without time zone NOT NULL,
  destroyed_at timestamp without time zone NULL
);
