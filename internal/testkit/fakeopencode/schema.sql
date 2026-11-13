CREATE TABLE `account_state` (
          `id` integer PRIMARY KEY,
          `active_account_id` text,
          `active_org_id` text,
          CONSTRAINT `fk_account_state_active_account_id_account_id_fk` FOREIGN KEY (`active_account_id`) REFERENCES `account`(`id`) ON DELETE SET NULL
        );
CREATE TABLE `account` (
          `id` text PRIMARY KEY,
          `email` text NOT NULL,
          `url` text NOT NULL,
          `access_token` text NOT NULL,
          `refresh_token` text NOT NULL,
          `token_expiry` integer,
          `time_created` integer NOT NULL,
          `time_updated` integer NOT NULL
        );
CREATE TABLE `control_account` (
          `email` text NOT NULL,
          `url` text NOT NULL,
          `access_token` text NOT NULL,
          `refresh_token` text NOT NULL,
          `token_expiry` integer,
          `active` integer NOT NULL,
          `time_created` integer NOT NULL,
          `time_updated` integer NOT NULL,
          CONSTRAINT `control_account_pk` PRIMARY KEY(`email`, `url`)
        );
CREATE TABLE `credential` (
          `id` text PRIMARY KEY,
          `integration_id` text,
          `label` text NOT NULL,
          `value` text NOT NULL,
          `connector_id` text,
          `method_id` text,
          `active` integer,
          `time_created` integer NOT NULL,
          `time_updated` integer NOT NULL
        );
CREATE TABLE `event_sequence` (
          `aggregate_id` text PRIMARY KEY,
          `seq` integer NOT NULL,
          `owner_id` text
        );
CREATE TABLE `event` (
          `id` text PRIMARY KEY,
          `aggregate_id` text NOT NULL,
          `seq` integer NOT NULL,
          `created` integer DEFAULT 0 NOT NULL,
          `type` text NOT NULL,
          `data` text NOT NULL,
          CONSTRAINT `fk_event_aggregate_id_event_sequence_aggregate_id_fk` FOREIGN KEY (`aggregate_id`) REFERENCES `event_sequence`(`aggregate_id`) ON DELETE CASCADE
        );
CREATE TABLE `job` (
          `notification_id` text PRIMARY KEY,
          `id` text NOT NULL,
          `session_id` text NOT NULL,
          `child_session_id` text,
          `kind` text NOT NULL,
          `title` text,
          `status` text NOT NULL,
          `output` text,
          `error` text,
          `recovery` text NOT NULL,
          `time_created` integer NOT NULL,
          `time_updated` integer NOT NULL
        );
CREATE TABLE `kv` (
          `key` text PRIMARY KEY,
          `value` text NOT NULL,
          `time_created` integer NOT NULL,
          `time_updated` integer NOT NULL
        );
CREATE TABLE `permission` (
          `id` text PRIMARY KEY,
          `project_id` text NOT NULL,
          `action` text NOT NULL,
          `resource` text NOT NULL,
          `time_created` integer NOT NULL,
          `time_updated` integer NOT NULL,
          CONSTRAINT `fk_permission_project_id_project_id_fk` FOREIGN KEY (`project_id`) REFERENCES `project`(`id`) ON DELETE CASCADE
        );
CREATE TABLE `project_directory` (
          `project_id` text NOT NULL,
          `directory` text NOT NULL,
          `type` text,
          `strategy` text,
          `time_created` integer NOT NULL,
          CONSTRAINT `project_directory_pk` PRIMARY KEY(`project_id`, `directory`),
          CONSTRAINT `fk_project_directory_project_id_project_id_fk` FOREIGN KEY (`project_id`) REFERENCES `project`(`id`) ON DELETE CASCADE
        );
CREATE TABLE `project` (
          `id` text PRIMARY KEY,
          `worktree` text NOT NULL,
          `vcs` text,
          `name` text,
          `icon_url` text,
          `icon_url_override` text,
          `icon_color` text,
          `time_created` integer NOT NULL,
          `time_updated` integer NOT NULL,
          `time_initialized` integer,
          `time_active` integer DEFAULT 0 NOT NULL,
          `time_archived` integer,
          `sandboxes` text NOT NULL,
          `commands` text
        );
CREATE TABLE `schedule` (
          `id` text PRIMARY KEY,
          `session_id` text,
          `name` text,
          `action` text NOT NULL,
          `check` text,
          `when` text NOT NULL,
          `tz` text NOT NULL,
          `status` text DEFAULT 'active' NOT NULL,
          `next_due_at` integer,
          `ends_at` integer,
          `remaining_runs` integer,
          `run_count` integer DEFAULT 0 NOT NULL,
          `checks` integer DEFAULT 0 NOT NULL,
          `failures` integer DEFAULT 0 NOT NULL,
          `last_fired_at` integer,
          `last_message_id` text,
          `last_outcome` text,
          `retry_at` integer,
          `state` text,
          `time_created` integer NOT NULL,
          `time_updated` integer NOT NULL,
          CONSTRAINT `fk_schedule_session_id_session_v2_id_fk` FOREIGN KEY (`session_id`) REFERENCES `session_v2`(`id`) ON DELETE CASCADE
        );
CREATE TABLE `blob_ref` (
          `session_id` text NOT NULL,
          `message_id` text NOT NULL,
          `hash` text NOT NULL,
          `seq` integer NOT NULL,
          CONSTRAINT `blob_ref_pk` PRIMARY KEY(`session_id`, `message_id`, `hash`),
          CONSTRAINT `fk_blob_ref_session_id_session_v2_id_fk` FOREIGN KEY (`session_id`) REFERENCES `session_v2`(`id`) ON DELETE CASCADE,
          CONSTRAINT `fk_blob_ref_hash_blob_hash_fk` FOREIGN KEY (`hash`) REFERENCES `blob`(`hash`) ON DELETE CASCADE
        );
CREATE TABLE `blob` (
          `hash` text PRIMARY KEY,
          `mime` text NOT NULL,
          `bytes` integer NOT NULL,
          `width` integer,
          `height` integer,
          `data` blob NOT NULL,
          `time_created` integer NOT NULL
        );
CREATE TABLE `instruction_blob` (
          `hash` text PRIMARY KEY,
          `value` text
        );
CREATE TABLE `instruction_entry` (
          `session_id` text NOT NULL,
          `key` text NOT NULL,
          `value` text,
          `removed` integer DEFAULT false NOT NULL,
          `time_created` integer NOT NULL,
          `time_updated` integer NOT NULL,
          CONSTRAINT `instruction_entry_pk` PRIMARY KEY(`session_id`, `key`),
          CONSTRAINT `fk_instruction_entry_session_id_session_v2_id_fk` FOREIGN KEY (`session_id`) REFERENCES `session_v2`(`id`) ON DELETE CASCADE
        );
CREATE TABLE `instruction_state` (
          `session_id` text PRIMARY KEY,
          `epoch_start` integer NOT NULL,
          `through_seq` integer NOT NULL,
          `initial_values` text NOT NULL,
          `current_values` text NOT NULL,
          CONSTRAINT `fk_instruction_state_session_id_session_v2_id_fk` FOREIGN KEY (`session_id`) REFERENCES `session_v2`(`id`) ON DELETE CASCADE
        );
CREATE TABLE `session_inbox` (
          `id` text PRIMARY KEY,
          `session_id` text NOT NULL,
          `type` text NOT NULL,
          `payload` text NOT NULL,
          `delivery` text NOT NULL,
          `enqueued_seq` integer NOT NULL,
          `time_created` integer NOT NULL,
          CONSTRAINT `fk_session_inbox_session_id_session_v2_id_fk` FOREIGN KEY (`session_id`) REFERENCES `session_v2`(`id`) ON DELETE CASCADE
        );
CREATE TABLE `session_message` (
          `id` text PRIMARY KEY,
          `session_id` text NOT NULL,
          `type` text NOT NULL,
          `seq` integer NOT NULL,
          `time_created` integer NOT NULL,
          `time_updated` integer NOT NULL,
          `data` text NOT NULL,
          CONSTRAINT `fk_session_message_session_id_session_v2_id_fk` FOREIGN KEY (`session_id`) REFERENCES `session_v2`(`id`) ON DELETE CASCADE
        );
CREATE TABLE `session_part` (
          `message_id` text NOT NULL,
          `session_id` text NOT NULL,
          `ordinal` integer NOT NULL,
          `type` text NOT NULL,
          `call_id` text,
          `time_created` integer NOT NULL,
          `time_updated` integer NOT NULL,
          `data` text NOT NULL,
          CONSTRAINT `session_part_pk` PRIMARY KEY(`message_id`, `ordinal`),
          CONSTRAINT `fk_session_part_message_id_session_message_id_fk` FOREIGN KEY (`message_id`) REFERENCES `session_message`(`id`) ON DELETE CASCADE,
          CONSTRAINT `fk_session_part_session_id_session_v2_id_fk` FOREIGN KEY (`session_id`) REFERENCES `session_v2`(`id`) ON DELETE CASCADE
        );
CREATE TABLE `session_v2` (
          `id` text PRIMARY KEY,
          `project_id` text NOT NULL,
          `workspace_id` text,
          `parent_id` text,
          `fork_session_id` text,
          `fork_boundary` text,
          `slug` text NOT NULL,
          `directory` text NOT NULL,
          `path` text,
          `title` text,
          `version` text NOT NULL,
          `share_url` text,
          `summary_additions` integer,
          `summary_deletions` integer,
          `summary_files` integer,
          `summary_diffs` text,
          `metadata` text,
          `cost` real DEFAULT 0 NOT NULL,
          `tokens_input` integer DEFAULT 0 NOT NULL,
          `tokens_output` integer DEFAULT 0 NOT NULL,
          `tokens_reasoning` integer DEFAULT 0 NOT NULL,
          `tokens_cache_read` integer DEFAULT 0 NOT NULL,
          `tokens_cache_write` integer DEFAULT 0 NOT NULL,
          `revert` text,
          `permission` text,
          `agent` text,
          `model` text,
          `time_created` integer NOT NULL,
          `time_updated` integer NOT NULL,
          `time_busy` integer,
          `time_idle` integer,
          `time_viewed` integer,
          `idle_outcome` text,
          `time_compacting` integer,
          `time_archived` integer,
          `time_suspended` integer,
          `resume_attempts` integer DEFAULT 0 NOT NULL,
          CONSTRAINT `fk_session_v2_project_id_project_id_fk` FOREIGN KEY (`project_id`) REFERENCES `project`(`id`) ON DELETE CASCADE
        );
CREATE TABLE `workspace` (
          `id` text PRIMARY KEY,
          `provider` text NOT NULL,
          `binding` text,
          `created_at` integer NOT NULL,
          `last_used_at` integer NOT NULL
        );
CREATE TABLE `worktree` (
          `project_id` text NOT NULL,
          `directory` text NOT NULL,
          `strategy` text,
          `time_created` integer NOT NULL,
          CONSTRAINT `worktree_pk` PRIMARY KEY(`project_id`, `directory`),
          CONSTRAINT `fk_worktree_project_id_project_id_fk` FOREIGN KEY (`project_id`) REFERENCES `project`(`id`) ON DELETE CASCADE
        );
CREATE UNIQUE INDEX `event_aggregate_seq_idx` ON `event` (`aggregate_id`,`seq`);
CREATE INDEX `event_aggregate_type_seq_idx` ON `event` (`aggregate_id`,`type`,`seq`);
CREATE INDEX `job_session_idx` ON `job` (`session_id`);
CREATE INDEX `job_child_session_idx` ON `job` (`child_session_id`);
CREATE INDEX `job_id_idx` ON `job` (`id`);
CREATE UNIQUE INDEX `permission_project_action_resource_idx` ON `permission` (`project_id`,`action`,`resource`);
CREATE INDEX `schedule_session_idx` ON `schedule` (`session_id`);
CREATE INDEX `schedule_due_idx` ON `schedule` (`next_due_at`) WHERE "schedule"."status" = 'active' and "schedule"."next_due_at" is not null;
CREATE INDEX `blob_ref_hash_idx` ON `blob_ref` (`hash`);
CREATE INDEX `blob_ref_session_seq_idx` ON `blob_ref` (`session_id`,`seq`);
CREATE INDEX `session_inbox_session_delivery_seq_idx` ON `session_inbox` (`session_id`,`delivery`,`enqueued_seq`);
CREATE UNIQUE INDEX `session_inbox_session_enqueued_seq_idx` ON `session_inbox` (`session_id`,`enqueued_seq`);
CREATE UNIQUE INDEX `session_message_session_seq_idx` ON `session_message` (`session_id`,`seq`);
CREATE INDEX `session_message_session_type_seq_idx` ON `session_message` (`session_id`,`type`,`seq`);
CREATE INDEX `session_message_session_time_created_id_idx` ON `session_message` (`session_id`,`time_created`,`id`);
CREATE INDEX `session_message_time_created_idx` ON `session_message` (`time_created`);
CREATE INDEX `session_part_session_message_idx` ON `session_part` (`session_id`,`message_id`,`ordinal`);
CREATE INDEX `session_v2_project_idx` ON `session_v2` (`project_id`);
CREATE INDEX `session_v2_workspace_idx` ON `session_v2` (`workspace_id`);
CREATE INDEX `session_v2_parent_idx` ON `session_v2` (`parent_id`);
CREATE INDEX `session_v2_time_suspended_idx` ON `session_v2` (`time_suspended`) WHERE "session_v2"."time_suspended" is not null;
CREATE TABLE "migration" (id TEXT PRIMARY KEY, time_completed INTEGER NOT NULL);
-- The migrations OpenCode 2.3.0 has run on a new database.
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260127222353_familiar_lady_ursula', 1794597293826);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260211171708_add_project_commands', 1794597293826);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260213144116_wakeful_the_professor', 1794597293826);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260225215848_workspace', 1794597293826);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260227213759_add_session_workspace_id', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260228203230_blue_harpoon', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260303231226_add_workspace_fields', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260309230000_move_org_to_state', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260312043431_session_message_cursor', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260323234822_events', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260410174513_workspace-name', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260413175956_chief_energizer', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260423070820_add_icon_url_override', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260427172553_slow_nightmare', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260428004200_add_session_path', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260501142318_next_venus', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260504145000_add_sync_owner', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260507164347_add_workspace_time', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260510033149_session_usage', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260511000411_data_migration_state', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260511173437_session-metadata', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260601010001_normalize_storage_paths', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260601202201_amazing_prowler', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260602002951_lowly_union_jack', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260602182828_add_project_directories', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260603001617_session_message_projection_indexes', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260603040000_session_message_projection_order', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260603141458_session_input_inbox', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260603160727_jittery_ezekiel_stane', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260604172448_event_sourced_session_input', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260605003541_add_session_context_snapshot', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260605042240_add_context_epoch_agent', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260611035744_credential', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260611192811_lush_chimera', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260612174303_project_dir_strategy', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260622142730_simplify_session_context_epoch', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260622170816_reset_v2_session_state', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260622202450_simplify_session_input', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260804233008_loose_psylocke', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260805200742_import_legacy_credentials', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260808023530_workspace_domain', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260811161259_execution_claim_attempts', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260812181746_session_inbox', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260812213948_worktree', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260819222447_session_viewed_state', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260823191254_nullable_workspace_binding', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260910120000_clear_v1_session_permission', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20260923013825_project_time_active', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20261007190000_azure_cli_external_credential', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20261010180456_project_time_archived', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20261024002554_session_allowance', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20261024175301_drop_session_allowance', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20261025123711_blob', 1794597293827);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20261025154125_drop_session_pending', 1794597293828);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20261026223005_schedule', 1794597293828);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20261029002625_session_part', 1794597293828);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20261029073502_session_time_busy', 1794597293828);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20261029165540_schedule_checks', 1794597293828);
INSERT INTO "migration" ("id", "time_completed") VALUES ('20261030054400_job', 1794597293828);
