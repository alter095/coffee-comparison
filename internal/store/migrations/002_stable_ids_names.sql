-- Preserve IDs, versions and timestamps while rebuilding tables atomically.
-- AUTOINCREMENT prevents a delayed edit from addressing a replacement row.
-- The explicit trim character set matches Go strings.TrimSpace (Unicode White_Space).
-- Invalid legacy names abort the transaction; never silently rewrite user data.
CREATE TABLE shops_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 200 AND instr(name,char(0))=0 AND length(trim(name,char(9,10,11,12,13,32,133,160,5760,8192,8193,8194,8195,8196,8197,8198,8199,8200,8201,8202,8232,8233,8239,8287,12288))) > 0),
 shipping_yen INTEGER CHECK(shipping_yen BETWEEN 0 AND 1000000),
 free_shipping_threshold_yen INTEGER CHECK(free_shipping_threshold_yen BETWEEN 0 AND 1000000),
 version INTEGER NOT NULL DEFAULT 1 CHECK(version > 0),
 created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;
CREATE TABLE beans_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 shop_id INTEGER NOT NULL REFERENCES shops_new(id) ON DELETE RESTRICT,
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 200 AND instr(name,char(0))=0 AND length(trim(name,char(9,10,11,12,13,32,133,160,5760,8192,8193,8194,8195,8196,8197,8198,8199,8200,8201,8202,8232,8233,8239,8287,12288))) > 0),
 product_url TEXT CHECK(length(product_url) <= 4096),
 status TEXT NOT NULL DEFAULT 'considering' CHECK(status IN ('considering','purchased')),
 version INTEGER NOT NULL DEFAULT 1 CHECK(version > 0),
 created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE TABLE variants_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 bean_id INTEGER NOT NULL REFERENCES beans_new(id) ON DELETE CASCADE,
 weight_g INTEGER NOT NULL CHECK(weight_g BETWEEN 1 AND 1000000),
 price_yen INTEGER NOT NULL CHECK(price_yen BETWEEN 0 AND 1000000),
 version INTEGER NOT NULL DEFAULT 1 CHECK(version > 0),
 created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(bean_id, weight_g)
) STRICT;

INSERT INTO shops_new SELECT * FROM shops;
INSERT INTO beans_new SELECT * FROM beans;
INSERT INTO variants_new SELECT * FROM variants;
DROP TABLE variants;
DROP TABLE beans;
DROP TABLE shops;
ALTER TABLE shops_new RENAME TO shops;
ALTER TABLE beans_new RENAME TO beans;
ALTER TABLE variants_new RENAME TO variants;
CREATE INDEX beans_shop_id ON beans(shop_id);
