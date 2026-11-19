-- Keep the former purchase flag for lossless migration; it is no longer used
-- by the API. Purchase does not imply exclusion or a wish to buy again.
ALTER TABLE beans RENAME COLUMN status TO legacy_purchase_status;
ALTER TABLE beans ADD COLUMN status TEXT NOT NULL DEFAULT 'candidate' CHECK(status IN ('candidate','excluded'));
ALTER TABLE beans ADD COLUMN buy_again INTEGER NOT NULL DEFAULT 0 CHECK(buy_again IN (0,1));
-- Invalidate snapshots from clients using the old status semantics.
UPDATE beans SET version=version+1;
