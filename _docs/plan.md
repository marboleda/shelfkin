# Shelfkin — Product Decisions (v1)

*Brainstorm summary — October 23, 2026*

Shelfkin is an app for managing a pantry: the ingredients a household has for cooking and baking.

## Vision

- Available as both a **web app and a mobile app**
- Multiple users can manage the **same pantry** when they belong to the same household
- UI should be **clean and simple, but pleasant to look at**

## Adding items

- **v1:** Fast manual entry, with type-ahead suggestions from an ingredient catalog
- **Later:**
  - Barcode scanning
  - Receipt / grocery-order import
  - Photo or voice input

## Quantities

- Tracked as **rough levels**: Full / Half / Low / Out
- Each entry has an **optional expiry date**

## Multiple purchases of the same item

- Each purchase is a **separate entry** (e.g. "Milk (Oct 20)" and "Milk (Oct 27)"), each with its own optional expiry

## Organization

- Every item has both a **storage location** (e.g. Pantry, Fridge, Freezer, Spice rack) and a **category** (e.g. Baking, Produce, Dairy, Grains, Spices)
- Users can **toggle the view** or filter by location or category
- Entries with the same name are **grouped into one row** in the view (e.g. "Milk ×2"), which expands to show each entry

## Item lifecycle

- Entries set to **Out stay visible** in the pantry
- Entries are removed **only when a user manually deletes them**

## Item catalog

- A **built-in catalog** of common ingredients, each with a default category, location and typical shelf life
- Users can add **custom items**, which are **private to the household**
- Members can set a custom item's **default category and location once**, and it is remembered for the household

## Households

- Users join a household via an **invite link or code**
- Roles: **owner** and **member**
  - The owner can manage members (e.g. remove them) and delete the household
- A user can belong to **multiple households** and switch between them

## Expiry alerts

- **In-app highlighting** of items expiring soon is always on
- **Push notifications** are opt-in for each user

## Offline

- **v1:** Read-only offline: the last-synced pantry can be viewed without a connection, but edits require one
- **Later:** Full offline editing that syncs on reconnect, with the last edit winning

## Change history

- **v1:** Each item shows who last updated it and when (e.g. "Set to Low by Sam, 2h ago")
- Change events are **stored from day one**, so a full household activity feed can be added later

## Out of scope for v1

- Shopping list
- Recipe matching / ingredient deduction

## Not yet decided

- Tech stack
- Screen designs and data model