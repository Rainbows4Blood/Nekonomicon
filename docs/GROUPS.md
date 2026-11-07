# D&D groups: evidence and rules (2026-11-06)

The user authorized fixed D&D edition grouping, including compatible products,
across DriveThruRPG and DM’s Guild. This is a local materialization, not a
configurable classification engine. B/X intentionally includes the broader
Basic D&D family (BECMI and Rules Cyclopedia), per the user's clarification.

## Catalog investigation

Read-only inspection covered the local schema-v3 catalog: 350,849 DTRPG and
47,021 DM’s Guild responses, with metadata extracted for both sources. The
investigation aggregated taxonomy IDs, translated labels, parents, taxonomy
groups and publisher identities, and inspected bounded product examples. No
network crawl, schema update or backfill was run on this catalog.

The scalar `edition` field is unsuitable: most values are NULL/empty, and `1.0`
usually identifies a product revision. Generic labels such as “1st Edition”
also occur in unrelated game systems. Category IDs and filter IDs collide.
Neither titles, descriptions, publication dates nor bare edition strings are
used as automatic rules.

Every selected filter ID was audited separately by source and kind. Shared
full-filter IDs have identical parent/group definitions in the stored data.
DTRPG additionally has sparse `storefrontPrimaryFilterValues`; these particular
IDs were not observed in that namespace on DM’s Guild. `internal/groups/taxonomy.go`
contains independent allowlists for the two sources. There is no fallback from
one source's namespace to the other. Present parent/group values must match the
audited definition. A changed context produces a warning and suppresses the
filter, including an abbreviated duplicate. An absent context can still use an
already verified source/kind/ID mapping. Category fallbacks are separately scoped
to the supported sources. This does not assert that the storefronts' entire
taxonomies are identical or that future taxonomy changes are impossible.

## Edition rules, version 1

Memberships can overlap when there is explicit evidence for multiple editions.
The first matching ID is stored as the membership's evidence. Numeric IDs below
are filters unless prefixed with `category:`. Primary-filter fallbacks apply only
to source/kind/ID combinations actually observed in the audit.

| Group | Accepted evidence |
| --- | --- |
| D&D 0 | 44698, 45344; category:9737 |
| B/X (Basic family) | 44699, 45345; category:2057, category:9736 |
| AD&D 1 | 1310, 44700, 44829, 45346 |
| AD&D 2 | 1320, 44701, 44830, 45347 |
| D&D 3 (3.0) | 44704, 44832, 45332, 45350 |
| D&D 3.5 | 44705, 44833, 45333, 45351 |
| D&D 4 | 44281 (DTRPG only), 44703, 44834, 45349 |
| D&D 5 | 45189, 45462, 45190 |
| D&D 5.5 | 1000260, 1000263 |

5.5 leaves inherit the fifth-edition parent tags 1000259/1000261. Those parents
alone do not create an extra D&D 5 membership when 5.5 is present. Separate 5e
leaf tags (45189/45462/45190) do permit both memberships. Generic fifth-edition parents or 5e-compatible tags without a specific revision
leaf remain unresolved with a warning, even when no 5.5 tag is present. Absence
of a 5.5 tag is not positive evidence of the 2014 revision. This intentionally
leaves some third-party fifth-edition products ungrouped. Missing revision
information cannot be recovered reliably through dates or description keywords.

“Basic/BECMI or OD&D” (1330/44828) alone cannot separate 0 from Basic. Likewise
1120/44702/45325/45348 (3.x/d20 or third-edition parents) cannot separate 3.0 from
3.5 or establish that a generic d20 game is D&D. These produce warnings until
more specific evidence exists. Unrelated products are processed successfully
with zero memberships. A D&D candidate with no supported edition evidence gets
a warning. The materialization includes the storefront's supplements, tools,
VTT products and bundles when tagged; it does not invent a separate book-format
filter or deduplicate the same product across stores.

## Official-product status

Status is `true`, `false`, or SQL NULL (unknown), with a fixed machine-readable
reason. It describes publisher/product evidence, not a legal licensing or canon
determination. The same logic runs for both independently keyed storefronts.

1. Community content or publisher 8957 (Dungeon Masters Guild) is false, even
   when tagged “Official D&D”. The catalog contains many such community listings.
2. Specific 5e-compatible (45326) or 4e/GSL (44281, DTRPG only) evidence is false.
3. Conflicting publisher IDs, or a WotC ID/name conflict, is unknown with a warning.
4. Publisher 44 (Wizards of the Coast) is true; the exact publisher name is a
   fallback only when both numeric publisher identifiers are absent.
5. Other identified publishers are false. Without publisher evidence, the broad
   OGL branch is false; otherwise status is unknown. Malformed community flags
   force unknown. Unknown status on a grouped product is visible as a warning.

Crucially, broad “D&D-OGL” (45322) and “3.x/d20/OGL” (1120) tags do not override
verified WotC publisher identity. The data places official products such as
Frostburn (1449) and Complete Warrior (1750) in that broad branch. Excluding all
OGL-labelled records would produce false negatives. Conversely, 45323 “Official
D&D” is never sufficient to mark a product official. Licensed products published
under other identities are not positively established as official by these
rules; additional evidence would require a reviewed rule revision.

## Storage and maintenance

Schema v4 explicitly adds `catalog_groups`, `product_groups`, `group_state`, and
`group_jobs`; it does not copy raw product JSON or backfill memberships. Opening
a catalog still only inspects compatibility. Apply database update first, then
run **Build D&D groups** for each storefront. Metadata extraction is independent:
the grouping task reads original responses through the shared extractor and does
not require a prior metadata backfill.

The nine group definitions are materialized at schema setup. Memberships store
source/product identity, official flag and evidence. State stores the grouping
version, response timestamp, official reason, outcome and bounded warnings.
Raw JSON, metadata, memberships, outcomes and the crawl success marker commit
atomically for new/refreshed products. Failed HTTP refreshes retain the previous
response and groups. Complete extraction failures retain previous memberships
as stale, excluded by `current_product_groups`. A successful refresh replaces
obsolete memberships, including removing all groups if evidence disappears.

The separate grouping maintenance pass uses the existing single write owner,
64-record/about-4-MiB response pages, short batch transactions, and a persisted
cursor committed with the outcomes. Pause, Resume, End and restart recovery
preserve committed work. Paused maintenance holds the catalog across both stores
and both transformations. Warning-only outcomes are complete; failures and
missing/stale/outdated outcomes are eligible for the next pass. UI issue pages
are capped at 50 and use text rendering. Opening never starts a maintenance job.

Group lookup uses `(group_id,source,product_id)`; official-only lookup adds the
`(group_id,source,official,product_id)` index. Replacement has a separate
`(source,product_id)` index. `Store.GroupMembers` provides bounded keyset pages,
including a combined-store mode that preserves separate source identities.
Freshness checks use product/state primary-key lookups; no response parsing or
full product-table scan occurs during group browsing. Maintenance coverage counts
still scan state/product keys on explicit refresh/open, not on every UI event.
There is no catalog browsing workspace in this change.

Example indexed query for future displays:

```sql
SELECT product_id, official, evidence
FROM current_product_groups
WHERE group_id = 'dnd-35' AND source = 'dtrpg' AND official = 1
  AND product_id > :last_id
ORDER BY product_id LIMIT 50;
```

Any semantic change, including relevant changes to the shared metadata extractor,
must bump the independent grouping version and update the current-view version
through an explicit schema migration. Existing records then become pending for
**Build D&D groups**. Do not silently reinterpret previously materialized rows.
