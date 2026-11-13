-- Fake rows in the tables hopsesh must never read (credentials, accounts, permission
-- grants, workspaces). Tests load them and prove no read reaches them.
INSERT INTO "credential" ("id", "integration_id", "label", "value", "connector_id", "method_id", "active", "time_created", "time_updated") VALUES ('cred_fake', 'openai', 'work', '{"type":"api","key":"hopsesh-fake-secret-never-read"}', NULL, 'api', 1, 1794597000000, 1794597000000);
INSERT INTO "account" ("id", "email", "url", "access_token", "refresh_token", "token_expiry", "time_created", "time_updated") VALUES ('acc_fake', 'alice@example.com', 'https://example.com', 'hopsesh-fake-secret-never-read', 'hopsesh-fake-secret-never-read', NULL, 1794597000000, 1794597000000);
INSERT INTO "control_account" ("email", "url", "access_token", "refresh_token", "token_expiry", "active", "time_created", "time_updated") VALUES ('alice@example.com', 'https://example.com', 'hopsesh-fake-secret-never-read', 'hopsesh-fake-secret-never-read', NULL, 1, 1794597000000, 1794597000000);
INSERT INTO "account_state" ("id", "active_account_id", "active_org_id") VALUES (1, 'acc_fake', NULL);
INSERT INTO "workspace" ("id", "provider", "binding", "created_at", "last_used_at") VALUES ('wrk_fake', 'fake', 'hopsesh-fake-secret-never-read', 1794597000000, 1794597000000);
INSERT INTO "permission" ("id", "project_id", "action", "resource", "time_created", "time_updated") VALUES ('per_fake', 'e1eece87d8935f099635f505ed301e85a3e1ae27', 'allow', 'hopsesh-fake-secret-never-read', 1794597000000, 1794597000000);
