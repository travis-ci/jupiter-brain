CREATE SEQUENCE instances_id_seq
  START WITH 1
  INCREMENT BY 1
  NO MINVALUE
  NO MAXVALUE
  CACHE 1;

CREATE TABLE instances (
  id bigint DEFAULT nextval('instances_id_seq'::regclass) NOT NULL,
  vsphere_id uuid UNIQUE NOT NULL,
  created_at timestamp without time zone NOT NULL
);
