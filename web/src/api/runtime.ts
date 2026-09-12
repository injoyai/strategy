export {
  DEFAULT_API_BASE_URL,
  createApiClient,
  newIdempotencyKey,
  normalizeApiError,
  type ApiError,
  type ApiClient,
  type CreateApiClientOptions,
  type NormalizedApiError,
} from "./client";

import { createApiClient } from "./client";

export const api = createApiClient();
