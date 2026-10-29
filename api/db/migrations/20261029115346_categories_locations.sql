-- Built-in lookup tables for categories and storage locations, with their seed
-- rows. See docs/data-model.md ("categories and locations").
--
-- IDs are set explicitly (no sequence) so they are the same in every database.
-- Keys are permanent identifiers that later code and seed data refer to; names
-- are display names and may change.

-- +goose Up
CREATE TABLE categories (
    id         smallint PRIMARY KEY,
    key        text     NOT NULL UNIQUE,
    name       text     NOT NULL,
    sort_order smallint NOT NULL
);

CREATE TABLE locations (
    id         smallint PRIMARY KEY,
    key        text     NOT NULL UNIQUE,
    name       text     NOT NULL,
    sort_order smallint NOT NULL
);

INSERT INTO categories (id, key, name, sort_order) VALUES
    (1,  'produce',      'Produce',             1),
    (2,  'dairy',        'Dairy',               2),
    (3,  'meat_seafood', 'Meat & seafood',      3),
    (4,  'grains',       'Grains',              4),
    (5,  'baking',       'Baking',              5),
    (6,  'spices',       'Spices',              6),
    (7,  'canned',       'Canned goods',        7),
    (8,  'condiments',   'Condiments & sauces', 8),
    (9,  'oils',         'Oils & vinegars',     9),
    (10, 'snacks',       'Snacks',              10),
    (11, 'beverages',    'Beverages',           11),
    (12, 'other',        'Other',               12);

INSERT INTO locations (id, key, name, sort_order) VALUES
    (1, 'pantry',     'Pantry',     1),
    (2, 'fridge',     'Fridge',     2),
    (3, 'freezer',    'Freezer',    3),
    (4, 'spice_rack', 'Spice rack', 4),
    (5, 'counter',    'Counter',    5);

-- +goose Down
DROP TABLE locations;
DROP TABLE categories;
