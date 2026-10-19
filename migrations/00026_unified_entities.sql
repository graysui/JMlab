-- +goose Up
-- Typed composite identities preserve old IDs even when different tables used
-- the same ID. Applications read compatibility views; all writes use unified tables.
CREATE TABLE entities (
    entity_kind TEXT NOT NULL CHECK (entity_kind IN ('people','studios','series','tags')),
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    aliases_json TEXT NOT NULL DEFAULT '[]',
    source_name TEXT,
    original_name TEXT NOT NULL DEFAULT '',
    avatar_url TEXT NOT NULL DEFAULT '',
    extra_attributes_json TEXT NOT NULL DEFAULT '{}',
    attributes_locked INTEGER NOT NULL DEFAULT 0 CHECK (attributes_locked IN (0,1)),
    gender TEXT,
    status TEXT NOT NULL DEFAULT 'unknown' CHECK (status IN ('active','retired','unknown')),
    birthday TEXT,
    height_cm INTEGER,
    cup_size TEXT,
    measurements_json TEXT,
    biography TEXT,
    avatar_image_id TEXT,
    source_url TEXT,
    kind TEXT NOT NULL DEFAULT 'custom' CHECK (kind IN ('genre','theme','source','custom')),
    parent_id TEXT,
    is_excluded INTEGER NOT NULL DEFAULT 0 CHECK (is_excluded IN (0,1)),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (entity_kind,id),
    FOREIGN KEY (entity_kind,parent_id) REFERENCES entities(entity_kind,id) ON DELETE RESTRICT,
    CHECK (parent_id IS NULL OR (entity_kind='tags' AND parent_id<>id))
);
CREATE TABLE entity_identifiers (
    entity_kind TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    namespace TEXT NOT NULL CHECK (namespace<>''),
    external_id TEXT NOT NULL CHECK (external_id<>''),
    PRIMARY KEY (entity_kind,namespace,external_id),
    FOREIGN KEY (entity_kind,entity_id) REFERENCES entities(entity_kind,id) ON DELETE CASCADE
);
CREATE INDEX idx_entity_identifiers_entity ON entity_identifiers(entity_kind,entity_id);
CREATE INDEX idx_entities_name ON entities(entity_kind,LOWER(name),id);
CREATE INDEX idx_entities_parent ON entities(entity_kind,parent_id);
CREATE TABLE item_entities (
    item_id TEXT NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
    entity_kind TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    role TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (item_id,entity_kind,entity_id,role),
    FOREIGN KEY (entity_kind,entity_id) REFERENCES entities(entity_kind,id) ON DELETE RESTRICT,
    CHECK ((entity_kind='people' AND role IN ('actor','director','writer','other'))
        OR (entity_kind='studios' AND role='studio')
        OR (entity_kind='series' AND role='series')
        OR (entity_kind='tags' AND role IN ('genre','tag')))
);
CREATE INDEX idx_item_entities_entity ON item_entities(entity_kind,entity_id,item_id,role);
CREATE INDEX idx_item_entities_item ON item_entities(item_id,entity_kind,sort_order,entity_id);

INSERT INTO entities (entity_kind,id,name,aliases_json,gender,status,birthday,height_cm,cup_size,measurements_json,biography,avatar_image_id,source_url,is_excluded,created_at,updated_at)
SELECT 'people',id,name,aliases_json,gender,status,birthday,height_cm,cup_size,measurements_json,biography,avatar_image_id,source_url,is_excluded,created_at,updated_at FROM people;
INSERT INTO entities (entity_kind,id,name,source_name,aliases_json,is_excluded,sort_order,created_at,updated_at)
SELECT 'studios',id,name,source_name,aliases_json,is_excluded,sort_order,created_at,updated_at FROM studios;
INSERT INTO entities (entity_kind,id,name,source_name,aliases_json,is_excluded,sort_order,created_at,updated_at)
SELECT 'series',id,name,source_name,aliases_json,is_excluded,sort_order,created_at,updated_at FROM series;
INSERT INTO entities (entity_kind,id,name,source_name,aliases_json,kind,is_excluded,sort_order,created_at,updated_at)
SELECT 'tags',id,name,source_name,aliases_json,kind,is_excluded,sort_order,created_at,updated_at FROM tags;
UPDATE entities SET parent_id=(SELECT t.parent_id FROM tags t WHERE t.id=entities.id) WHERE entity_kind='tags';
INSERT INTO item_entities SELECT item_id,'people',person_id,role,sort_order FROM item_people;
INSERT INTO item_entities SELECT item_id,'studios',studio_id,'studio',sort_order FROM item_studios;
INSERT INTO item_entities SELECT item_id,'series',series_id,'series',sort_order FROM item_series;
INSERT INTO item_entities SELECT it.item_id,'tags',it.tag_id,CASE WHEN t.kind='genre' THEN 'genre' ELSE 'tag' END,0 FROM item_tags it JOIN tags t ON t.id=it.tag_id;

CREATE TABLE user_person_states_next (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    person_id TEXT NOT NULL,
    entity_kind TEXT NOT NULL DEFAULT 'people' CHECK (entity_kind='people'),
    is_favorite INTEGER NOT NULL DEFAULT 0 CHECK (is_favorite IN (0,1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id,person_id),
    FOREIGN KEY (entity_kind,person_id) REFERENCES entities(entity_kind,id) ON DELETE CASCADE
);
INSERT INTO user_person_states_next (user_id,person_id,is_favorite,created_at,updated_at) SELECT user_id,person_id,is_favorite,created_at,updated_at FROM user_person_states;
DROP TABLE user_person_states;
ALTER TABLE user_person_states_next RENAME TO user_person_states;
DROP TABLE item_people;
DROP TABLE item_studios;
DROP TABLE item_series;
DROP TABLE item_tags;
UPDATE tags SET parent_id=NULL;
DROP TABLE people;
DROP TABLE studios;
DROP TABLE series;
DROP TABLE tags;

CREATE VIEW people AS SELECT id,name,aliases_json,gender,status,birthday,height_cm,cup_size,measurements_json,biography,avatar_image_id,source_url,is_excluded,created_at,updated_at,original_name,avatar_url,extra_attributes_json,attributes_locked FROM entities WHERE entity_kind='people';
CREATE VIEW studios AS SELECT id,name,source_name,aliases_json,is_excluded,sort_order,created_at,updated_at,original_name,avatar_url,extra_attributes_json,attributes_locked FROM entities WHERE entity_kind='studios';
CREATE VIEW series AS SELECT id,name,source_name,aliases_json,is_excluded,sort_order,created_at,updated_at,original_name,avatar_url,extra_attributes_json,attributes_locked FROM entities WHERE entity_kind='series';
CREATE VIEW tags AS SELECT id,name,source_name,aliases_json,kind,parent_id,is_excluded,sort_order,created_at,updated_at,original_name,avatar_url,extra_attributes_json,attributes_locked FROM entities WHERE entity_kind='tags';
CREATE VIEW item_people AS SELECT item_id,entity_id AS person_id,role,sort_order FROM item_entities WHERE entity_kind='people';
CREATE VIEW item_studios AS SELECT item_id,entity_id AS studio_id,sort_order FROM item_entities WHERE entity_kind='studios';
CREATE VIEW item_series AS SELECT item_id,entity_id AS series_id,sort_order FROM item_entities WHERE entity_kind='series';
CREATE VIEW item_tags AS SELECT DISTINCT item_id,entity_id AS tag_id FROM item_entities WHERE entity_kind='tags';

-- +goose Down
-- Writer roles and external identities cannot be represented by the old model.
CREATE TABLE unified_entity_downgrade_guard (count INTEGER CHECK (count=0));
INSERT INTO unified_entity_downgrade_guard VALUES (1);
