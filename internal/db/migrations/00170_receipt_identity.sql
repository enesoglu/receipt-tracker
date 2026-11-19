-- +goose Up

-- The e-archive identity printed on chain receipts: the invoice number
-- (FATURA NO or BELGE NO), the ETTN and the seller's tax number. All three
-- are optional — an ordinary shop's receipt prints none — and a value the
-- reader could not certify stays NULL rather than being guessed. The tax
-- number is text, because a VKN may start with zero; the invoice number
-- keeps the letters and digits a chain prints, trimmed; the ETTN is a UUID
-- in its canonical spelling. No uniqueness: two photos of one paper receipt
-- carry the same values, and both are kept.
ALTER TABLE receipts
    ADD COLUMN invoice_number text,
    ADD COLUMN ettn uuid,
    ADD COLUMN seller_tax_number text,
    ADD CONSTRAINT receipts_invoice_number_check CHECK (
        invoice_number IS NULL
        OR (btrim(invoice_number) = invoice_number
            AND invoice_number ~ '^[0-9A-Z]{1,32}$')),
    ADD CONSTRAINT receipts_seller_tax_number_check CHECK (
        seller_tax_number IS NULL OR seller_tax_number ~ '^[0-9]{10}$');

-- +goose Down
ALTER TABLE receipts
    DROP CONSTRAINT receipts_seller_tax_number_check,
    DROP CONSTRAINT receipts_invoice_number_check,
    DROP COLUMN seller_tax_number,
    DROP COLUMN ettn,
    DROP COLUMN invoice_number;
