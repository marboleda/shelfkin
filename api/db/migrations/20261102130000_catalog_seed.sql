-- The built-in ingredient catalog: common cooking and baking ingredients, each
-- with a default category, default location and typical shelf life (#8). See
-- docs/data-model.md (catalog_items).
--
-- Built-in rows have household_id and created_by NULL and take their id from
-- the table's uuidv7() default. Later migrations refer to a built-in item by
-- its name, which is unique. Adding or correcting built-in items is done in a
-- new migration; this one is never edited once applied.
--
-- Naming convention:
--   - Sentence case: only the first word is capitalized ("Brown sugar", not
--     "Brown Sugar"). Proper names and words made from them keep their
--     capital ("Dijon mustard", "Greek yogurt", "Parmesan cheese").
--   - The form people write on a shopping list: plural for things bought by
--     the piece ("Eggs", "Lemons"), the plain name otherwise ("Milk",
--     "Rolled oats", "All-purpose flour").
--   - Generic names, no brand names.
--   - No leading or trailing spaces.
--   - No two names that differ only by case or by plural ("Egg" and "Eggs").
--   - One English name per item, no regional variants.
--
-- shelf_life_days is the typical life after purchase in the default location,
-- as a whole number of days from 1 to 3650. It is NULL for items that in
-- practice do not expire (salt, sugar, honey, vinegar).
--
-- Rows name their category and location by key (#6), never by numeric id. The
-- LEFT JOINs turn a mistyped key into NULL, so the insert fails on the NOT NULL
-- columns instead of silently dropping the row.
--
-- One ingredient per line, grouped by category and sorted by name within each
-- group.

-- +goose Up
INSERT INTO catalog_items (name, default_category_id, default_location_id, shelf_life_days)
SELECT v.name, c.id, l.id, v.shelf_life_days
FROM (VALUES
    -- produce
    ('Apples',                    'produce',      'fridge',       42),
    ('Avocados',                  'produce',      'counter',       4),
    ('Bananas',                   'produce',      'counter',       5),
    ('Bell peppers',              'produce',      'fridge',       10),
    ('Blueberries',               'produce',      'fridge',        7),
    ('Broccoli',                  'produce',      'fridge',        5),
    ('Cabbage',                   'produce',      'fridge',       30),
    ('Carrots',                   'produce',      'fridge',       28),
    ('Cauliflower',               'produce',      'fridge',        7),
    ('Celery',                    'produce',      'fridge',       14),
    ('Cilantro',                  'produce',      'fridge',        7),
    ('Cucumbers',                 'produce',      'fridge',        7),
    ('Fresh basil',               'produce',      'fridge',        7),
    ('Fresh ginger',              'produce',      'fridge',       21),
    ('Fresh rosemary',            'produce',      'fridge',       14),
    ('Fresh thyme',               'produce',      'fridge',       10),
    ('Frozen berries',            'produce',      'freezer',     270),
    ('Frozen corn',               'produce',      'freezer',     300),
    ('Frozen peas',               'produce',      'freezer',     300),
    ('Frozen spinach',            'produce',      'freezer',     300),
    ('Garlic',                    'produce',      'pantry',      120),
    ('Grapes',                    'produce',      'fridge',       10),
    ('Green beans',               'produce',      'fridge',        7),
    ('Green onions',              'produce',      'fridge',        7),
    ('Jalapeños',                 'produce',      'fridge',       10),
    ('Kale',                      'produce',      'fridge',        5),
    ('Lemons',                    'produce',      'fridge',       28),
    ('Lettuce',                   'produce',      'fridge',        7),
    ('Limes',                     'produce',      'fridge',       28),
    ('Mint',                      'produce',      'fridge',        7),
    ('Mushrooms',                 'produce',      'fridge',        7),
    ('Onions',                    'produce',      'pantry',       45),
    ('Oranges',                   'produce',      'fridge',       21),
    ('Parsley',                   'produce',      'fridge',       10),
    ('Pears',                     'produce',      'counter',       5),
    ('Potatoes',                  'produce',      'pantry',       30),
    ('Red onions',                'produce',      'pantry',       45),
    ('Shallots',                  'produce',      'pantry',       30),
    ('Spinach',                   'produce',      'fridge',        5),
    ('Strawberries',              'produce',      'fridge',        4),
    ('Sweet potatoes',            'produce',      'pantry',       21),
    ('Tomatoes',                  'produce',      'counter',       5),
    ('Zucchini',                  'produce',      'fridge',        7),

    -- dairy
    ('Almond milk',               'dairy',        'fridge',        7),
    ('Butter',                    'dairy',        'fridge',       60),
    ('Buttermilk',                'dairy',        'fridge',       14),
    ('Cheddar cheese',            'dairy',        'fridge',       42),
    ('Cottage cheese',            'dairy',        'fridge',       10),
    ('Cream cheese',              'dairy',        'fridge',       21),
    ('Eggs',                      'dairy',        'fridge',       28),
    ('Feta cheese',               'dairy',        'fridge',       21),
    ('Greek yogurt',              'dairy',        'fridge',       14),
    ('Half-and-half',             'dairy',        'fridge',       10),
    ('Heavy cream',               'dairy',        'fridge',       10),
    ('Ice cream',                 'dairy',        'freezer',      60),
    ('Milk',                      'dairy',        'fridge',        7),
    ('Mozzarella cheese',         'dairy',        'fridge',       21),
    ('Oat milk',                  'dairy',        'fridge',        7),
    ('Parmesan cheese',           'dairy',        'fridge',       90),
    ('Ricotta cheese',            'dairy',        'fridge',        7),
    ('Sour cream',                'dairy',        'fridge',       14),
    ('Swiss cheese',              'dairy',        'fridge',       30),
    ('Unsalted butter',           'dairy',        'fridge',       60),
    ('Yogurt',                    'dairy',        'fridge',       14),

    -- meat_seafood
    ('Bacon',                     'meat_seafood', 'fridge',        7),
    ('Chicken breasts',           'meat_seafood', 'fridge',        2),
    ('Chicken thighs',            'meat_seafood', 'fridge',        2),
    ('Deli turkey',               'meat_seafood', 'fridge',        5),
    ('Frozen fish fillets',       'meat_seafood', 'freezer',     180),
    ('Frozen shrimp',             'meat_seafood', 'freezer',     180),
    ('Ground beef',               'meat_seafood', 'fridge',        2),
    ('Ground pork',               'meat_seafood', 'fridge',        2),
    ('Ground turkey',             'meat_seafood', 'fridge',        2),
    ('Ham',                       'meat_seafood', 'fridge',        5),
    ('Pepperoni',                 'meat_seafood', 'fridge',       21),
    ('Pork chops',                'meat_seafood', 'fridge',        3),
    ('Salmon fillets',            'meat_seafood', 'fridge',        2),
    ('Sausages',                  'meat_seafood', 'fridge',        3),
    ('Steak',                     'meat_seafood', 'fridge',        4),
    ('Whole chicken',             'meat_seafood', 'fridge',        2),

    -- grains
    ('Arborio rice',              'grains',       'pantry',      730),
    ('Bagels',                    'grains',       'counter',       5),
    ('Barley',                    'grains',       'pantry',      365),
    ('Basmati rice',              'grains',       'pantry',      730),
    ('Bread',                     'grains',       'counter',       5),
    ('Bread crumbs',              'grains',       'pantry',      180),
    ('Brown rice',                'grains',       'pantry',      180),
    ('Cereal',                    'grains',       'pantry',      180),
    ('Corn tortillas',            'grains',       'fridge',       21),
    ('Couscous',                  'grains',       'pantry',      365),
    ('Dried black beans',         'grains',       'pantry',      730),
    ('Dried lentils',             'grains',       'pantry',      730),
    ('Egg noodles',               'grains',       'pantry',      730),
    ('Flour tortillas',           'grains',       'pantry',       14),
    ('Jasmine rice',              'grains',       'pantry',      730),
    ('Lasagna noodles',           'grains',       'pantry',      730),
    ('Macaroni',                  'grains',       'pantry',      730),
    ('Panko',                     'grains',       'pantry',      180),
    ('Penne',                     'grains',       'pantry',      730),
    ('Pita bread',                'grains',       'counter',       5),
    ('Quinoa',                    'grains',       'pantry',      730),
    ('Ramen noodles',             'grains',       'pantry',      365),
    ('Rice noodles',              'grains',       'pantry',      730),
    ('Rolled oats',               'grains',       'pantry',      365),
    ('Spaghetti',                 'grains',       'pantry',      730),
    ('Steel-cut oats',            'grains',       'pantry',      365),
    ('White rice',                'grains',       'pantry',     1460),

    -- baking
    ('Active dry yeast',          'baking',       'pantry',      365),
    ('All-purpose flour',         'baking',       'pantry',      270),
    ('Almond flour',              'baking',       'pantry',       90),
    ('Baking chocolate',          'baking',       'pantry',      730),
    ('Baking powder',             'baking',       'pantry',      365),
    ('Baking soda',               'baking',       'pantry',      540),
    ('Bread flour',               'baking',       'pantry',      270),
    ('Brown sugar',               'baking',       'pantry',     NULL),
    ('Cake flour',                'baking',       'pantry',      270),
    ('Chocolate chips',           'baking',       'pantry',      365),
    ('Cocoa powder',              'baking',       'pantry',      730),
    ('Corn syrup',                'baking',       'pantry',     1095),
    ('Cornmeal',                  'baking',       'pantry',      365),
    ('Cornstarch',                'baking',       'pantry',     NULL),
    ('Cream of tartar',           'baking',       'pantry',     1460),
    ('Gelatin',                   'baking',       'pantry',     1095),
    ('Molasses',                  'baking',       'pantry',      730),
    ('Powdered sugar',            'baking',       'pantry',     NULL),
    ('Shredded coconut',          'baking',       'pantry',      180),
    ('Sprinkles',                 'baking',       'pantry',     1095),
    ('Sugar',                     'baking',       'pantry',     NULL),
    ('Vanilla extract',           'baking',       'pantry',     NULL),
    ('Whole wheat flour',         'baking',       'pantry',       90),

    -- spices
    ('Bay leaves',                'spices',       'spice_rack', 1095),
    ('Black peppercorns',         'spices',       'spice_rack', 1460),
    ('Cayenne pepper',            'spices',       'spice_rack', 1095),
    ('Chili powder',              'spices',       'spice_rack', 1095),
    ('Curry powder',              'spices',       'spice_rack', 1095),
    ('Dried basil',               'spices',       'spice_rack', 1095),
    ('Dried dill',                'spices',       'spice_rack', 1095),
    ('Dried oregano',             'spices',       'spice_rack', 1095),
    ('Dried parsley',             'spices',       'spice_rack', 1095),
    ('Dried rosemary',            'spices',       'spice_rack', 1095),
    ('Dried thyme',               'spices',       'spice_rack', 1095),
    ('Garam masala',              'spices',       'spice_rack', 1095),
    ('Garlic powder',             'spices',       'spice_rack', 1095),
    ('Ground allspice',           'spices',       'spice_rack', 1095),
    ('Ground black pepper',       'spices',       'spice_rack', 1095),
    ('Ground cinnamon',           'spices',       'spice_rack', 1095),
    ('Ground cloves',             'spices',       'spice_rack', 1095),
    ('Ground coriander',          'spices',       'spice_rack', 1095),
    ('Ground cumin',              'spices',       'spice_rack', 1095),
    ('Ground ginger',             'spices',       'spice_rack', 1095),
    ('Ground nutmeg',             'spices',       'spice_rack', 1095),
    ('Ground turmeric',           'spices',       'spice_rack', 1095),
    ('Italian seasoning',         'spices',       'spice_rack', 1095),
    ('Kosher salt',               'spices',       'spice_rack', NULL),
    ('Onion powder',              'spices',       'spice_rack', 1095),
    ('Paprika',                   'spices',       'spice_rack', 1095),
    ('Red pepper flakes',         'spices',       'spice_rack', 1095),
    ('Salt',                      'spices',       'spice_rack', NULL),
    ('Sea salt',                  'spices',       'spice_rack', NULL),
    ('Sesame seeds',              'spices',       'pantry',      365),
    ('Smoked paprika',            'spices',       'spice_rack', 1095),

    -- canned
    ('Beef broth',                'canned',       'pantry',      365),
    ('Canned black beans',        'canned',       'pantry',     1095),
    ('Canned chickpeas',          'canned',       'pantry',     1095),
    ('Canned corn',               'canned',       'pantry',     1095),
    ('Canned diced tomatoes',     'canned',       'pantry',      545),
    ('Canned kidney beans',       'canned',       'pantry',     1095),
    ('Canned peaches',            'canned',       'pantry',      545),
    ('Canned pumpkin',            'canned',       'pantry',     1095),
    ('Canned salmon',             'canned',       'pantry',     1095),
    ('Canned tuna',               'canned',       'pantry',     1095),
    ('Chicken broth',             'canned',       'pantry',      365),
    ('Coconut milk',              'canned',       'pantry',      730),
    ('Cream of mushroom soup',    'canned',       'pantry',      730),
    ('Crushed tomatoes',          'canned',       'pantry',      545),
    ('Evaporated milk',           'canned',       'pantry',      365),
    ('Refried beans',             'canned',       'pantry',      730),
    ('Sweetened condensed milk',  'canned',       'pantry',      365),
    ('Tomato paste',              'canned',       'pantry',      545),
    ('Tomato sauce',              'canned',       'pantry',      545),
    ('Vegetable broth',           'canned',       'pantry',      365),

    -- condiments
    ('Barbecue sauce',            'condiments',   'fridge',      120),
    ('Capers',                    'condiments',   'fridge',      365),
    ('Chocolate hazelnut spread', 'condiments',   'pantry',      180),
    ('Dijon mustard',             'condiments',   'fridge',      365),
    ('Fish sauce',                'condiments',   'pantry',      730),
    ('Hoisin sauce',              'condiments',   'fridge',      365),
    ('Honey',                     'condiments',   'pantry',     NULL),
    ('Hot sauce',                 'condiments',   'pantry',      730),
    ('Ketchup',                   'condiments',   'fridge',      180),
    ('Lemon juice',               'condiments',   'fridge',      180),
    ('Maple syrup',               'condiments',   'fridge',      365),
    ('Mayonnaise',                'condiments',   'fridge',       60),
    ('Miso paste',                'condiments',   'fridge',      365),
    ('Olives',                    'condiments',   'fridge',       90),
    ('Pasta sauce',               'condiments',   'pantry',      365),
    ('Peanut butter',             'condiments',   'pantry',       90),
    ('Pesto',                     'condiments',   'fridge',        7),
    ('Pickles',                   'condiments',   'fridge',       90),
    ('Ranch dressing',            'condiments',   'fridge',       60),
    ('Relish',                    'condiments',   'fridge',      180),
    ('Salsa',                     'condiments',   'fridge',       14),
    ('Soy sauce',                 'condiments',   'pantry',      730),
    ('Sriracha',                  'condiments',   'fridge',      365),
    ('Strawberry jam',            'condiments',   'fridge',      180),
    ('Tahini',                    'condiments',   'pantry',      180),
    ('Teriyaki sauce',            'condiments',   'fridge',      365),
    ('Worcestershire sauce',      'condiments',   'pantry',      730),
    ('Yellow mustard',            'condiments',   'fridge',      365),

    -- oils
    ('Apple cider vinegar',       'oils',         'pantry',     NULL),
    ('Avocado oil',               'oils',         'pantry',      365),
    ('Balsamic vinegar',          'oils',         'pantry',     NULL),
    ('Canola oil',                'oils',         'pantry',      365),
    ('Coconut oil',               'oils',         'pantry',      730),
    ('Cooking spray',             'oils',         'pantry',      730),
    ('Olive oil',                 'oils',         'pantry',      540),
    ('Peanut oil',                'oils',         'pantry',      365),
    ('Red wine vinegar',          'oils',         'pantry',     NULL),
    ('Rice vinegar',              'oils',         'pantry',     NULL),
    ('Sesame oil',                'oils',         'pantry',      365),
    ('Shortening',                'oils',         'pantry',      365),
    ('Vegetable oil',             'oils',         'pantry',      365),
    ('White vinegar',             'oils',         'pantry',     NULL),

    -- snacks
    ('Almonds',                   'snacks',       'pantry',      365),
    ('Cashews',                   'snacks',       'pantry',      180),
    ('Crackers',                  'snacks',       'pantry',      180),
    ('Dark chocolate',            'snacks',       'pantry',      365),
    ('Dried apricots',            'snacks',       'pantry',      180),
    ('Granola',                   'snacks',       'pantry',      180),
    ('Peanuts',                   'snacks',       'pantry',      180),
    ('Pecans',                    'snacks',       'pantry',      180),
    ('Popcorn kernels',           'snacks',       'pantry',      730),
    ('Potato chips',              'snacks',       'pantry',       60),
    ('Pretzels',                  'snacks',       'pantry',       90),
    ('Pumpkin seeds',             'snacks',       'pantry',      180),
    ('Raisins',                   'snacks',       'pantry',      180),
    ('Rice cakes',                'snacks',       'pantry',      180),
    ('Sunflower seeds',           'snacks',       'pantry',      180),
    ('Tortilla chips',            'snacks',       'pantry',       60),
    ('Trail mix',                 'snacks',       'pantry',      180),
    ('Walnuts',                   'snacks',       'pantry',      180),

    -- beverages
    ('Apple juice',               'beverages',    'fridge',       10),
    ('Beer',                      'beverages',    'fridge',      180),
    ('Coconut water',             'beverages',    'pantry',      365),
    ('Coffee beans',              'beverages',    'pantry',      180),
    ('Dry white wine',            'beverages',    'pantry',      730),
    ('Green tea',                 'beverages',    'pantry',      365),
    ('Ground coffee',             'beverages',    'pantry',       90),
    ('Hot chocolate mix',         'beverages',    'pantry',      365),
    ('Instant coffee',            'beverages',    'pantry',      730),
    ('Orange juice',              'beverages',    'fridge',        7),
    ('Red wine',                  'beverages',    'pantry',      730),
    ('Soda',                      'beverages',    'pantry',      270),
    ('Sparkling water',           'beverages',    'pantry',      365),
    ('Tea bags',                  'beverages',    'pantry',      730),

    -- other
    ('Tofu',                      'other',        'fridge',       30)
) AS v (name, category_key, location_key, shelf_life_days)
LEFT JOIN categories c ON c.key = v.category_key
LEFT JOIN locations l ON l.key = v.location_key;

-- +goose Down
-- This is the only migration that adds built-in items, so the built-in rows
-- are exactly the rows it inserted. Household overrides of them are removed
-- with them (on delete cascade).
DELETE FROM catalog_items WHERE household_id IS NULL;
