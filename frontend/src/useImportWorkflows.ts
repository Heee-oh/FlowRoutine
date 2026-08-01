import { useCallback, useRef, useState } from "react";

import { parseHarArchive } from "./harImport";
import { assertImportFileSize } from "./importValidation";
import { parsePostmanCollection } from "./postmanImport";
import type {
  RequestImportPreview,
  RequestImportSource,
} from "./importGraph";
import type {
  OpenAPIEndpoint,
  OpenAPIImportRequest,
  OpenAPIImportResponse,
} from "./types";
import { importOpenAPI } from "./wails";

export function useOpenAPIImportWorkflow(
  onSelectEndpoint: (endpoint: OpenAPIEndpoint, sourceURL: string) => void,
) {
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [imported, setImported] = useState<OpenAPIImportResponse | null>(null);

  const reset = useCallback(() => {
    setError("");
    setMessage("");
    setImported(null);
  }, []);

  const show = useCallback(() => {
    reset();
    setOpen(true);
  }, [reset]);

  const close = useCallback(() => {
    if (!loading) {
      setOpen(false);
      reset();
    }
  }, [loading, reset]);

  const submit = useCallback(async (request: OpenAPIImportRequest) => {
    setLoading(true);
    reset();
    try {
      const response = await importOpenAPI(request);
      setImported(response);
      setMessage(`Loaded ${response.title || "OpenAPI document"} (${response.openapi}) with ${response.endpoints.length} endpoints`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load OpenAPI document");
    } finally {
      setLoading(false);
    }
  }, [reset]);

  const selectEndpoint = useCallback((endpoint: OpenAPIEndpoint) => {
    if (!imported) {
      return;
    }
    onSelectEndpoint(endpoint, imported.sourceUrl);
    setOpen(false);
    reset();
  }, [imported, onSelectEndpoint, reset]);

  return { close, error, imported, loading, message, open, selectEndpoint, show, submit };
}

export function useRequestImportWorkflow(onError: (message: string) => void) {
  const [pending, setPending] = useState<RequestImportPreview | null>(null);
  const [error, setError] = useState("");
  const nextID = useRef(1);

  const prepare = useCallback(async (source: RequestImportSource, file: File) => {
    const label = source === "Postman" ? "Postman collection" : "HAR file";
    try {
      onError("");
      setError("");
      assertImportFileSize(file.size, label);
      const raw = await file.text();
      const requests = source === "Postman"
        ? parsePostmanCollection(raw)
        : parseHarArchive(raw);
      setPending({
        id: nextID.current,
        fileName: file.name,
        fileSize: file.size,
        requests,
        source,
      });
      nextID.current += 1;
    } catch (err) {
      setPending(null);
      onError(err instanceof Error ? err.message : `Failed to read ${label}`);
    }
  }, [onError]);

  const importPostman = useCallback((file: File) => {
    void prepare("Postman", file);
  }, [prepare]);

  const importHAR = useCallback((file: File) => {
    void prepare("HAR", file);
  }, [prepare]);

  const clear = useCallback(() => {
    setPending(null);
    setError("");
  }, []);

  return { clear, error, importHAR, importPostman, pending, setError };
}
