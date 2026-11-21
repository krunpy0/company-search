BEGIN;

INSERT INTO datasets VALUES
    ('rsmp', 'FNS', 'https://example.invalid/sme', 'test'),
    ('revexp', 'FNS', 'https://example.invalid/financials', 'test'),
    ('sshr2019', 'FNS', 'https://example.invalid/headcount', 'test'),
    ('snrip', 'FNS', 'https://example.invalid/ip-regimes', 'test'),
    ('snr', 'FNS', 'https://example.invalid/regimes', 'test'),
    ('paytax', 'FNS', 'https://example.invalid/payments', 'test'),
    ('debtam', 'FNS', 'https://example.invalid/debts', 'test');

INSERT INTO releases (dataset_code, published_on, period_key, download_url, schema_url, schema_version, status)
VALUES
    ('rsmp', '2026-11-10', 'current', '', '', 'fixture-v1', 'published'),
    ('revexp', '2026-10-25', '2025', '', '', 'fixture-v1', 'published'),
    ('sshr2019', '2026-10-25', '2025', '', '', 'fixture-v1', 'published'),
    ('snrip', '2026-10-25', '2026-10-01', '', '', 'fixture-v1', 'published'),
    ('snr', '2026-10-25', '2026-10-01', '', '', 'fixture-v1', 'published'),
    ('paytax', '2026-04-01', '2025', '', '', 'fixture-v1', 'published'),
    ('debtam', '2026-10-25', '2026-10-01', '', '', 'fixture-v1', 'published');

INSERT INTO published_releases SELECT dataset_code, period_key, id FROM releases;
INSERT INTO registrations VALUES
    ('1073906000376', 'legal', '3907056353'),
    ('326000000000037', 'entrepreneur', '500100732259'),
    ('326000000000048', 'entrepreneur', '500100732259');

INSERT INTO sme_observations (release_id, reg_no, display_name, region_code, as_of, included_on,
    category, subject_type, source_doc_id, source_file)
SELECT id, reg_no, display_name, '39', '2026-11-10', '2026-01-01', '1', '1', reg_no, 'sme.xml'
FROM releases CROSS JOIN (VALUES
    ('1073906000376', 'ООО Гефест'), ('326000000000037', 'ИП Тест 1'), ('326000000000048', 'ИП Тест 2')
) AS subjects(reg_no, display_name) WHERE dataset_code = 'rsmp';

INSERT INTO financials
SELECT id, inn, display_name, 2025, '2025-12-31', '2026-10-25', 'finance-test', 'finance.xml', 0.00, 0.00
FROM releases CROSS JOIN (VALUES ('3907056353', 'ООО Гефест'), ('7707083893', 'Финансовая запись')) AS subjects(inn, display_name)
WHERE dataset_code = 'revexp';

INSERT INTO tax_regime_observations
SELECT id, '0522014502', NULL, 'Тестовый налогоплательщик', ARRAY['usn'],
    '2026-10-01', '2026-10-25', '2b6870dc-5c2b-4d27-b5f2-c29e81ef92ac', 'regime.xml'
FROM releases WHERE dataset_code = 'snr';

INSERT INTO tax_payments
SELECT id, '7838080688', 'Налог на добавленную стоимость', 0.00,
    '2025-12-31', '2026-04-01', '620ab455-a239-46c9-9fd7-12175c6ce2f0', 'payment.xml', 1
FROM releases WHERE dataset_code = 'paytax';

INSERT INTO tax_debts
SELECT id, '6612025806', tax_name, amount, 0.00, NULL, amount,
    '2026-10-01', '2026-10-25', 'd96b0983-4a38-4522-b149-443b9a502cab', 'debt.xml', line
FROM releases CROSS JOIN (VALUES ('НДС', 221695.72, 1), ('Страховые взносы', 5.00, 2)) AS lines(tax_name, amount, line)
WHERE dataset_code = 'debtam';

INSERT INTO registrations VALUES
    ('1230000000009', 'legal', '6230092529'),
    ('325366800093867', 'entrepreneur', '900000000083');

INSERT INTO registrations
SELECT prefix || (prefix::bigint % 13 % 10)::text, 'entrepreneur', '900000000083'
FROM (SELECT '326123' || lpad(n::text, 8, '0') AS prefix FROM generate_series(1, 15) n) AS ids;

INSERT INTO sme_observations (release_id, reg_no, display_name, region_code, as_of, included_on,
    category, subject_type, employees, employee_year, source_doc_id, source_file)
SELECT id, reg_no, 'Тест МСП', '39', '2026-11-10', '2026-01-01', '1', '1', 1, 2025, reg_no, 'sme.xml'
FROM releases CROSS JOIN registrations
WHERE dataset_code = 'rsmp' AND (reg_no = '1230000000009' OR reg_no LIKE '326123%');

INSERT INTO employee_observations
SELECT id, inn, 'Тест численности', 2025, 0, '2025-12-31', '2026-10-25',
    'a3b9755c-2394-4e5d-8d8b-b4b720648d02', 'employees.xml'
FROM releases CROSS JOIN (VALUES ('7327032797'), ('6230092529')) AS subjects(inn)
WHERE dataset_code = 'sshr2019';

INSERT INTO tax_regime_observations
SELECT id, '900000000083', '325366800093867', 'ИП Тест', ARRAY['usn', 'psn'],
    '2026-10-01', '2026-10-25', 'b510ed8a-7199-492d-9b3b-b8d6887e9a28', 'regime.xml'
FROM releases WHERE dataset_code = 'snrip';

INSERT INTO tax_regime_quarantine
SELECT snrip.id, sme.reg_no, '500100732259', '900000000083', sme.release_id, 'ИП Тест', ARRAY['usn'],
    '2026-10-01', '2026-10-25', sme.reg_no, 'regime.xml'
FROM releases snrip CROSS JOIN sme_observations sme
WHERE snrip.dataset_code = 'snrip' AND sme.reg_no LIKE '326123%';

COMMIT;
