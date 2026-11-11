/**
 * Collection schema calls: list, read, set and delete a collection's
 * metadata schema, and dry-run validate metadata against it. The server
 * takes and returns the schema as a JSON *string* (an object is a 400), so
 * these methods pass the editor's text through unchanged.
 */
import { postJson } from './http';

export const schemaMethods = {
  /** Every collection with a schema: `{ schemas: [{ collection, schema }] }`. */
  listSchemas() {
    return postJson(this, '/schema/list', {});
  },

  /** One collection's schema: `{ collection, schema, enabled }` (`schema` "" when none). */
  getSchema(collection) {
    return postJson(this, '/schema/get', { collection });
  },

  /** Sets the schema from its JSON text. */
  setSchema(collection, schemaText) {
    return postJson(this, '/schema/set', { collection, schema: schemaText });
  },

  /** Removes the schema; validation of that collection stops. */
  deleteSchema(collection) {
    return postJson(this, '/schema/delete', { collection });
  },

  /** Validates `meta` without storing anything: `{ valid, errors, warnings }`. */
  validateMeta(collection, meta) {
    return postJson(this, '/validate', { collection, meta });
  },
};
