# Shelfkin — Screen designs

The screen designs for v1 (task 23 in [tasks.md](tasks.md)) and the design tokens they use. The tokens are applied to the app theme in `app/src/theme/tokens.ts` ([#44](https://github.com/marboleda/shelfkin/issues/44)).

## Canvas

Design canvas: https://claude.ai/artifact/HMaouUb2fNFf2LdKAg98aC#page-dcb81aee8d83

The canvas is private and may not open for everyone. Every value needed to build the app is recorded in this file, so you do not need to open it.

## Artboards

| Artboard | What it shows |
|---|---|
| Pantry | The pantry list on a phone: entries grouped into sections, level and expiry, the add button and the bottom tab bar |
| Add item — search | First step of adding an item: search the household's items or start a new one |
| Add item — details | Second step of adding an item: location, level, expiry date and the save button |
| Entry detail | One pantry entry with its details, edit controls and the remove action |
| Household switcher | The menu for switching between the households you belong to |
| Sign in | The sign-in screen with the app mark and the sign-in options |
| Onboarding — no household yet | What a new user sees before belonging to a household: create one or join one |
| Join via invite link | Accepting an invite link to join an existing household |
| Settings (owner) | Household and notification settings as the household owner sees them |
| Invite people (owner) | The owner creating and sharing an invite link |
| Web — wide layout | The pantry at desktop width, with the side nav on the left |
| Foundations & components | Colour tokens in light and dark, the type scale, radii, spacing, and shared components (buttons, chips, level meter, inputs, segmented controls) |

## Colour tokens

Light and dark values. Token names match `app/src/theme/tokens.ts`.

| Token | Light | Dark | Use |
|---|---|---|---|
| `background` | `#F7F6F2` | `#141513` | App and screen background |
| `surface` | `#FFFFFF` | `#1E201D` | Cards, rows, tab bar, side nav |
| `text` | `#1C1B19` | `#ECEAE4` | Body text |
| `mutedText` | `#5F5C55` | `#A6A39B` | Captions, hints, inactive nav labels, section overlines |
| `border` | `#DDD9CF` | `#363830` | Hairlines, outlines, card borders |
| `primary` | `#2F6B4F` | `#7BC5A0` | Primary action, active nav label |
| `onPrimary` | `#FFFFFF` | `#0F2A1D` | Text and icons on `primary` |
| `primarySubtle` | `#E3EFE8` | `#23382C` | Active nav pill / item background |
| `danger` | `#B3261E` | `#F2B8B5` | Destructive actions, errors, expired text |
| `dangerSubtle` | `#F9E1DE` | `#3A1F1D` | Background of the "Expired" chip |
| `expiringSoon` | `#8A4B00` | `#F5B74A` | "Expires soon" text |
| `expiringSubtle` | `#FBEBD9` | `#3A2A14` | Background of the "Expires soon" chip |
| `levelLow` | `#C27A12` | `#E0A04A` | Filled bar of the level meter at Low (a graphic, never text) |
| `levelEmpty` | `#E4E0D6` | `#3A3C35` | Unfilled bars of the level meter |

Every text colour meets 4.5:1 against the background it is used on, in both modes. `levelLow` and `levelEmpty` are used only for graphics, never for text; `levelEmpty` on `surface` is under 3:1 by design, because the level meter always shows the level as a word too.

## Type

Figtree, falling back to the platform's system font. Same in both modes.

| Token | Size / line height | Weight | Other |
|---|---|---|---|
| `heading` | 28 / 34 | 700 | |
| `title` | 20 / 26 | 700 | |
| `body` | 16 / 24 | 400 | |
| `label` | 15 / 20 | 600 | |
| `caption` | 13 / 18 | 400 | |
| `overline` | 13 / 18 | 700 | Uppercase, letter spacing 0.6. Section headers such as "FRIDGE" |

## Radii

| Token | Value |
|---|---|
| `sm` | 6 |
| `md` | 10 |
| `card` | 12 |
| `lg` | 16 |
| `pill` | 999 |

## Spacing

| Token | Value |
|---|---|
| `xs` | 4 |
| `sm` | 8 |
| `md` | 12 |
| `lg` | 16 |
| `xl` | 24 |
| `xxl` | 32 |

## Navigation

The app switches layout at a window width of 768 px.

- Below 768 px: bottom tab bar on `surface` with a `border` top hairline. Each tab is an icon above its label. The active tab's icon sits in a `primarySubtle` pill (radius `pill`) and its icon and label use `primary`; inactive tabs use `mutedText`
- At 768 px and wider: side nav on `surface` with a `border` right hairline. From the top: the Shelfkin name, the household switcher (#26), then Pantry and Settings. The active item has a `primarySubtle` background (radius `md`) with a `primary` label; inactive items use `mutedText`

## Notes

- Dark-mode screens were not drawn. Only the dark token values exist, so check dark mode against the token tables rather than an artboard
- The app mark on Sign in and Web — wide layout is a placeholder. The final artwork is [#64](https://github.com/marboleda/shelfkin/issues/64)
- Some artboards use one-off colours and weights that are not tokens, for example `#234F3B` for the active nav label, `#E6E2D8` card borders, `#9C1F18` expired chip text and weight 800 for the wordmark. These map to the nearest token (`primary`, `border`, `danger`, and the `heading` weight 700) and are not added to the theme
- Where a design and an issue's acceptance criteria disagree, the acceptance criteria win
