# Shelfkin — v1 Backlog

*Created October 26, 2026*

Product decisions are in [plan.md](plan.md) and the schema is in [data-model.md](data-model.md). The repository layout (set up in task 1) is: `api/` for the Go API, `app/` for the Expo app (web, iOS, Android), `api/db/migrations/` for SQL migrations, and `openapi/` for the API spec. Every task should leave the full test suite passing.

All tasks except task 23 have moved to GitHub issues, where they are tracked. Task numbers after 22 differ from issue numbers.

| Task | Issue | Title |
|---|---|---|
| 1 | [#1](https://github.com/marboleda/shelfkin/issues/1) | Set up an empty project with a passing test |
| 2 | [#2](https://github.com/marboleda/shelfkin/issues/2) | Run tests in CI |
| 3 | [#3](https://github.com/marboleda/shelfkin/issues/3) | Local Postgres and migration tooling |
| 4 | [#4](https://github.com/marboleda/shelfkin/issues/4) | Users and push tokens schema |
| 5 | [#5](https://github.com/marboleda/shelfkin/issues/5) | Households, members and invites schema |
| 6 | [#6](https://github.com/marboleda/shelfkin/issues/6) | Categories and locations with seed data |
| 7 | [#7](https://github.com/marboleda/shelfkin/issues/7) | Catalog and household item defaults schema |
| 8 | [#8](https://github.com/marboleda/shelfkin/issues/8) | Seed the built-in ingredient catalog |
| 9 | [#9](https://github.com/marboleda/shelfkin/issues/9) | Pantry entries, events and notification log schema |
| 10 | [#10](https://github.com/marboleda/shelfkin/issues/10) | Row-level security and the app database role |
| 11 | [#11](https://github.com/marboleda/shelfkin/issues/11) | OpenAPI spec and code generation |
| 12 | [#12](https://github.com/marboleda/shelfkin/issues/12) | Go API server skeleton |
| 13 | [#13](https://github.com/marboleda/shelfkin/issues/13) | Firebase Auth verification and current user |
| 14 | [#14](https://github.com/marboleda/shelfkin/issues/14) | Households API |
| 15 | [#15](https://github.com/marboleda/shelfkin/issues/15) | Household members API |
| 16 | [#16](https://github.com/marboleda/shelfkin/issues/16) | Invites API |
| 17 | [#17](https://github.com/marboleda/shelfkin/issues/17) | Catalog search API |
| 18 | [#18](https://github.com/marboleda/shelfkin/issues/18) | Custom items and household defaults API |
| 19 | [#19](https://github.com/marboleda/shelfkin/issues/19) | Pantry entry writes with change events |
| 20 | [#20](https://github.com/marboleda/shelfkin/issues/20) | Pantry read API |
| 21 | [#21](https://github.com/marboleda/shelfkin/issues/21) | Push token and notification settings API |
| 22 | [#22](https://github.com/marboleda/shelfkin/issues/22) | Expiry notification job |
| 24 | [#23](https://github.com/marboleda/shelfkin/issues/23) | App shell, navigation and theme |
| 25 | [#24](https://github.com/marboleda/shelfkin/issues/24) | App API client and data layer |
| 26 | [#25](https://github.com/marboleda/shelfkin/issues/25) | Sign-in and sign-up in the app |
| 27 | [#26](https://github.com/marboleda/shelfkin/issues/26) | Household onboarding and switcher |
| 28 | [#27](https://github.com/marboleda/shelfkin/issues/27) | Pantry list screen |
| 29 | [#28](https://github.com/marboleda/shelfkin/issues/28) | Location/category view toggle and filters |
| 30 | [#29](https://github.com/marboleda/shelfkin/issues/29) | Add item flow |
| 31 | [#30](https://github.com/marboleda/shelfkin/issues/30) | Edit and delete entries |
| 32 | [#31](https://github.com/marboleda/shelfkin/issues/31) | Item defaults editing |
| 33 | [#32](https://github.com/marboleda/shelfkin/issues/32) | Household settings screen |
| 34 | [#33](https://github.com/marboleda/shelfkin/issues/33) | Invite deep links |
| 35 | [#34](https://github.com/marboleda/shelfkin/issues/34) | Push notification opt-in in the app |
| 36 | [#35](https://github.com/marboleda/shelfkin/issues/35) | Read-only offline mode |
| 37 | [#36](https://github.com/marboleda/shelfkin/issues/36) | Keeping data fresh |
| 38 | [#37](https://github.com/marboleda/shelfkin/issues/37) | GCP and Firebase infrastructure |
| 39 | [#38](https://github.com/marboleda/shelfkin/issues/38) | API deployment pipeline |
| 40 | [#39](https://github.com/marboleda/shelfkin/issues/39) | Web app deployment |
| 41 | [#40](https://github.com/marboleda/shelfkin/issues/40) | Schedule the expiry job in production |
| 42 | [#41](https://github.com/marboleda/shelfkin/issues/41) | Mobile builds and test distribution |

## Not tracked in GitHub

Screen designs are handled separately.

### 23. Screen designs
Goal: Agreed wireframes and a visual direction for every v1 screen.
Description: Design the sign-in, onboarding (create or join household), pantry list (grouped rows, location/category toggle, filters, expiry highlighting), add item, entry detail/edit, household settings and notification settings screens for phone and desktop widths. Define a small set of design tokens (colors, type scale, spacing) that is clean and simple but pleasant. Record the result in `_docs/` (images or links plus notes).
