-- A bean's version represents its metadata and capacity/price contents.
-- Invalidate stale parent edits/deletions when any child changes. The trigger
-- and original write run in the same SQLite transaction.
CREATE TRIGGER variants_insert_bean_version AFTER INSERT ON variants BEGIN
 UPDATE beans SET version=version+1,updated_at=CURRENT_TIMESTAMP WHERE id=NEW.bean_id;
END;
CREATE TRIGGER variants_update_bean_version AFTER UPDATE ON variants BEGIN
 UPDATE beans SET version=version+1,updated_at=CURRENT_TIMESTAMP WHERE id=NEW.bean_id;
END;
CREATE TRIGGER variants_delete_bean_version AFTER DELETE ON variants BEGIN
 UPDATE beans SET version=version+1,updated_at=CURRENT_TIMESTAMP WHERE id=OLD.bean_id;
END;
