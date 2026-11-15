-- +goose Up

-- How the prices of a product are compared (docs/ARCHITECTURE.md §4):
-- 'item' compares packs, so its size matters; 'kg' and 'l' make a generic
-- product ("chicken fillet") that stands for every brand and pack size of a
-- thing and is compared per kilogram or litre. Such a product has no
-- package size of its own.
ALTER TABLE products
    ADD COLUMN price_basis text NOT NULL DEFAULT 'item',
    ADD CONSTRAINT products_price_basis_check CHECK (price_basis IN ('item', 'kg', 'l'));

-- A product whose purchases were all weighed is already used as a generic
-- product: switch it to 'kg'. Units are stored as the receipt prints them
-- ('KG', 'Kg', 'gr'), so they are compared without case.
UPDATE products p
SET price_basis = 'kg', size_value = NULL, size_unit = NULL
WHERE EXISTS (SELECT 1 FROM line_items l WHERE l.user_id = p.user_id AND l.product_id = p.id)
  AND NOT EXISTS (
      SELECT 1 FROM line_items l
      WHERE l.user_id = p.user_id AND l.product_id = p.id
        AND (l.quantity_unit IS NULL
             OR lower(btrim(l.quantity_unit)) NOT IN ('kg', 'kgr', 'kilo', 'kilogram', 'g', 'gr', 'grm', 'gram'))
  );

ALTER TABLE products
    ADD CONSTRAINT products_price_basis_size_check CHECK (price_basis = 'item' OR size_value IS NULL);

-- +goose Down

-- Sizes cleared by the switch to 'kg' are not restored: the products they
-- belonged to were compared per kilogram, and their lines keep the sizes.
ALTER TABLE products
    DROP CONSTRAINT products_price_basis_size_check,
    DROP CONSTRAINT products_price_basis_check,
    DROP COLUMN price_basis;
