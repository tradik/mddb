/**
 * The client methods added in 2.19.0, grouped one module per feature and
 * mixed into the MDDB client's prototype at the end of mddb-client.js. Each
 * method uses the client's own `request`/`postForm`, so authentication and
 * error handling stay in one place.
 */
import { memoryMethods } from './memory';
import { schemaMethods } from './schemas';
import { bulkIngestMethods } from './bulk-ingest';
import { apiKeyMethods } from './api-keys';
import { aggregateMethods } from './aggregate';
import { duplicateMethods } from './duplicates';
import { batchMethods } from './batch';
import { importMethods } from './imports';
import { maintenanceMethods } from './maintenance';
import { featureMethods } from './features'; // GO-049

export const adminMethods = {
  ...memoryMethods,
  ...schemaMethods,
  ...bulkIngestMethods,
  ...apiKeyMethods,
  ...aggregateMethods,
  ...duplicateMethods,
  ...batchMethods,
  ...importMethods,
  ...maintenanceMethods,
  ...featureMethods,
};
