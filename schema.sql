CREATE SCHEMA jupiter_brain;

CREATE SEQUENCE jupiter_brain.instances_id_seq
  START WITH 1
  INCREMENT BY 1
  NO MINVALUE
  NO MAXVALUE
  CACHE 1;

CREATE TABLE jupiter_brain.instances (
  id bigint DEFAULT nextval('jupiter_brain.instances_id_seq'::regclass) NOT NULL,
  vsphere_id uuid UNIQUE NOT NULL,
  created_at timestamp without time zone NOT NULL
);
