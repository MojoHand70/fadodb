# Schemas and objects

A document describes things of the world: the person who asked, the
depot a delivery leaves from, the address it goes to. An **object** is
one value with named fields. A **schema** is the list of those fields
under a name, defined once and used by every class that needs it. This
chapter is the reference for both: how a schema is defined, the schemas
the engine brings, an object in a class, and what a class can and
cannot do with an object today. A test defines what stands here and
plays it on each push.

## Four ways to keep a contact on a document

| Kept as | What it is | Choose it when |
|---|---|---|
| loose variables: `contact_name`, `contact_email` | values that nothing holds together | there are one or two |
| a `json` variable | any JSON, kept as it is | the shape is not known |
| an object, by a schema | one value with typed fields, read as `contact.email` | the thing has a fixed shape and no life of its own |
| a `ref` to a document of another class | a pointer | the thing has a life of its own: states, a history, its own editors |

An object has no identity. A person on two documents is two values,
and a document's history shows the person as they were written down at
each version.

## A schema

A schema is written once, as one JSON document, like a class.

```json
{
  "name": "Contact",
  "fields": {
    "name":  "text",
    "email": "text",
    "phone": "text"
  },
  "presentation": {"title": "name", "subtitle": "email"}
}
```

A field may hold an object of another schema, the engine's or yours:

```json
{
  "name": "Depot",
  "fields": {
    "code":     "text",
    "contact":  "object<Contact>",
    "address":  "object<Address>",
    "capacity": "int",
    "rent":     "money(2)"
  }
}
```

| Word | Meaning |
|---|---|
| `name` | the name of the schema: letters, digits and underscores. A class names it, in any case of letters |
| `fields` | each field with its type: any simple type but `json`, written as in a table, or `object<Name>` for an object of another schema |
| `extends` | the name of one other schema. The fields of that schema come first, and this one adds its own |
| `presentation` | how a screen shows an object of this schema: `title` and `subtitle`, formulas over the fields that give text; `avatar`, the name of a text field with a picture; `icon` and `color` |

- **A field holds one value.** A list or rows belong on the class, not
  in a schema.
- **A schema that another one names is defined first.**
- **Objects nest at most four deep** below a variable.
- **A schema and a class may have the same name.** They do not meet.
- **A field has a type and nothing else**: no `required`, no default,
  no list of values.

The simple types are those of a column, in [Tables](tables.md#the-types-of-a-column).

## The schemas the engine brings

Nine schemas are there in every database. Their names are taken: a
schema of yours may not have one of them.

<!-- the schemas the engine brings -->
| Schema | Fields |
|---|---|
| `Person` | `id` text, `name` text, `email` text, `phone` text, `avatar` text |
| `Address` | `line1` text, `line2` text, `city` text, `postal_code` text, `region` text, `country` text |
| `Organization` | `id` text, `name` text, `tax_id` text, `email` text, `phone` text, `address` Address |
| `GeoPoint` | `lat` number, `lon` number |
| `Place` | `name` text, `address` Address, `geo` GeoPoint |
| `Period` | `start` datetime, `end` datetime |
| `Event` | `title` text, `start` datetime, `end` datetime, `all_day` bool, `place` Place, `organizer` Person |
| `Quantity` | `value` decimal(6), `unit` text |
| `File` | `hash` text, `name` text, `media_type` text, `size` int |

The engine keeps what it is given in them and checks the types only.
It does not know people, so `Person.id` is any text; a host program
that writes its caller's id there can ask `driver.id = USER()`. `File`
is the value of an attached file; the chapter on files is planned.

## An object in a class

A variable of the type `object` names its schema. A `rows` variable may
name a schema too, and then each of its rows is an object of it.

```json
{
  "name": "Delivery",
  "title": "\"Delivery to \" & depot.address.city",
  "variables": {
    "depot":  {"type": "object", "schema": "Depot"},
    "driver": {"type": "object", "schema": "Person"},
    "stops":  {"type": "rows", "schema": "Contact"},
    "city":   {"formula": "depot.address.city"},
    "roomy":  {"formula": "depot.capacity > 100"}
  },
  "states": {"initial": "planned", "list": ["planned", "sent"], "final": ["sent"]},
  "transitions": [
    {"name": "send", "from": "planned", "to": "sent",
     "guard": "NOT ISBLANK(depot.code) AND NOT ISBLANK(driver.name)"}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "INGROUP(\"everyone\")",
    "delete": "FALSE"
  },
  "rights": {
    "*": {"driver": {"write": ["dispatch"]}}
  }
}
```

## The values

A caller gives an object as a map of its fields, and leaves out what
it does not know.

<!-- the values Ola gives -->
```jsonc
{
  "depot": {
    "code": "GDA",
    "contact": {"name": "Ewa", "email": "ewa@example.com"},
    "address": {"city": "Gdańsk", "country": "PL"},
    "capacity": 120,
    "rent": "1000.00 PLN"
  },
  "stops": [
    {"name": "Adam"},
    {"name": "Beata", "email": "beata@example.com"}
  ]
}
```

The engine answers with every field, a blank one as `null`:

<!-- the values as they are answered -->
```jsonc
{
  "city": "Gdańsk",
  "depot": {
    "address": {
      "city": "Gdańsk",
      "country": "PL",
      "line1": null,
      "line2": null,
      "postal_code": null,
      "region": null
    },
    "capacity": "120",
    "code": "GDA",
    "contact": {
      "email": "ewa@example.com",
      "name": "Ewa",
      "phone": null
    },
    "rent": "1000.00 PLN"
  },
  "driver": null,
  "roomy": true,
  "stops": [
    {
      "email": null,
      "name": "Adam",
      "phone": null
    },
    {
      "email": "beata@example.com",
      "name": "Beata",
      "phone": null
    }
  ]
}
```

- **An object with no value in any field is `null`**, as `driver` is.
- **An object is replaced whole.** A change gives the whole object
  again; a field it leaves out is blank afterwards. No call changes
  one field of an object.
- **Each field is checked like a value of its type.**

## What it does

Dan is a designer. Ola plans deliveries. Marek is in the group
`dispatch`.

<!-- what it does -->
| Step | Who | Does | Answer |
|---|---|---|---|
| 1 | Dan | defines the schema `Contact`, and then `Depot` | defined, version 1 and version 1 |
| 2 | Ola | creates a delivery with the values above | title "Delivery to Gdańsk", `city` Gdańsk, `roomy` TRUE |
| 3 | Ola | gives the depot a field the schema does not have | refused, `unknown_variable`, variable `depot.floor` |
| 4 | Ola | gives the depot the capacity "many" | refused, `bad_value`, variable `depot.capacity` |
| 5 | Ola | gives the depot again, with the code WAW and the city Warszawa only | title "Delivery to Warszawa", `city` Warszawa, `roomy` FALSE |
| 6 | Ola | reads the contact of the depot | blank |
| 7 | Ola | sends the delivery | refused, `guard_false` |
| 8 | Ola | names the driver | refused, `forbidden`, variable `driver` |
| 9 | Marek | names the driver: Jan | allowed |
| 10 | Ola | sends the delivery | allowed |
| 11 | Ola | counts the deliveries whose depot is in Warszawa | 1 |

Step 5 gave no capacity and no contact, so both are blank afterwards,
and `roomy` is FALSE.

## What a class can do with an object

This is what is built today. The rest of the design is in
[ADR-0031](../adr/0031-named-schemas-and-object-values.md).

<!-- what a class can do with an object -->
| A class can | Today |
|---|---|
| read one field in a formula, at any depth: `depot.address.city` | yes |
| use a field in a title, a guard, a line of `access`, a right, a rule | yes |
| find documents by a field: `depot.address.city = "Warszawa"` | yes |
| say who may write or see the object as a whole | yes |
| say who may write or see one field of it | no, `unknown_variable` |
| name the object in a rule on `values_change`, or reset it in a rule | yes |
| set an object, or one field of it, in a rule | no, `not_supported`, `unknown_variable` |
| test or compare a whole object: `ISBLANK(depot)`, `a = b` | no, `bad_formula` |
| compute an object | no, `bad_formula` |
| give an object a default | no, `not_supported` |
| keep an index on an object | no, `not_supported` |
| read a field of `rows` in a formula: `stops.name` | no, `bad_formula` |

So today a formula reads fields, one at a time. To ask whether an
object is there, test the field that matters: `NOT ISBLANK(depot.code)`.

> **`required` does not hold for an object.** The engine accepts
> `"required": true` on a variable of the type `object`, a list or
> `rows`, and does not enforce it: a document is created without the
> value. Until that is settled, guard the transition that needs the
> object with a test of one of its fields, as `send` does.

## How an object is stored

An object is no column of its own. Each field is a column, named by
the path to it with two underscores between the parts. SQL shows them
so:

<!-- the columns -->
| Field | Column of the view `Delivery` |
|---|---|
| `depot.code` | `depot__code` |
| `depot.contact.email` | `depot__contact__email` |
| `depot.address.city` | `depot__address__city` |
| `depot.rent` | `depot__rent`, and `depot__rent_currency` for the currency |
| `driver.name` | `driver__name` |

The rows of `stops` are in a view of their own, `Delivery_stops`, with
one column for each field of `Contact`. So a report reads a city or an
e-mail address as a typed column, with no JSON to take apart.

That is why a name of a schema, a field or a variable may not contain
two underscores in a row, and why a field name may not end in
`_currency`.

## Changing a schema

<!-- changing a schema -->
| The schema | A changed definition |
|---|---|
| is used by no class | is taken, as the next version |
| is used by a class | is refused, `not_supported` |
| is one of the engine's | is refused, `reserved_name` |

- **The same definition again changes nothing.**
- **Every version says who defined it and when**, and every caller may
  read that.
- **Who may define a schema**: the system group `designers`, as for a
  class.
- **No call removes a schema.**

For a schema in use, the engine's advice is to define the new shape
under another name.

## The presentation

The `presentation` of a schema says how a screen shows an object: a
title, a line under it, a picture, an icon and a colour. The engine
checks it when the schema is defined and keeps it. Nothing in the
engine reads it today: it is there for the screens of Phase 7.

## The commands

<!-- the commands -->
| To | Command |
|---|---|
| define a schema | `define_schema` |
| read its definition back | `get_schema` |
| list the schemas, the engine's first | `list_schemas` |

What each takes and what it answers is in
[The commands](commands.md#definitions).

## What is checked when the schema is defined

<!-- checked when the schema is defined -->
| The schema | Code |
|---|---|
| has no name, or a name that is not letters, digits and underscores | `bad_name` |
| has the name of one of the engine's schemas | `reserved_name` |
| has the name of another schema in another case of letters | `duplicate` |
| has no fields | `missing` |
| has two fields whose names differ only in the case of letters | `duplicate` |
| has a field of a type the engine does not know | `unknown_type` |
| has a `json` field | `not_allowed` |
| has a field that is a list or rows | `not_allowed` |
| has a field of a schema the database does not have | `unknown_schema` |
| contains itself | `schema_cycle` |
| nests more than four deep | `too_deep` |
| has a field with a name the engine's tables use: `doc_id`, `version`, `idx` | `reserved_name` |
| has a field whose name contains two underscores in a row, or ends in `_currency` | `bad_name` |
| extends a schema the database does not have | `unknown_schema` |
| gives a field the name of a field of the schema it extends | `duplicate` |
| has a `title` or a `subtitle` that reads a field it does not have, or the caller or the clock | `bad_formula` |
| has a `title` or a `subtitle` that does not give text | `type_mismatch` |
| names an `avatar` that is no field of it | `unknown_variable` |
| has a word the engine does not know | `bad_json` |

And in a class:

<!-- checked when the class is defined -->
| The class | Code |
|---|---|
| has an object variable that names no schema | `missing` |
| names a schema the database does not have | `unknown_schema` |
| writes the fields of an object into the variable, in the place of the name of a schema | `bad_json` |
| reads a field the schema does not have | `bad_formula` |

## What is refused

<!-- what is refused -->
| The caller | Code | The refusal names |
|---|---|---|
| gives a field the schema does not have | `unknown_variable` | the path, as `depot.floor` |
| gives a value that does not fit its field | `bad_value` | the path, as `depot.capacity` |
| gives something that is not a map of fields | `bad_value` | the variable, as `depot` |
| names a field as if it were a variable | `unknown_variable` | the name given, as `depot.code` |
| defines a schema and is no designer | `forbidden` | `/system/designers` |

## Not here yet

- Whole objects in formulas: `ISBLANK(depot)`, `a = b`, a computed
  object, an object as the value a rule sets.
- A field of `rows` in a formula, and so the functions over lists.
- `required` on an object, a list or rows.
- A default for an object, and an index on one of its fields.
- Rights for single fields.
- Changing a schema that a class uses, and removing one.
- The screens that use `presentation`.
