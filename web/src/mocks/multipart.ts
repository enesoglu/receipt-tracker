export interface UploadedMockFile {
  name: string;
  type: string;
  size: number;
}

const MAX_CAPTURED = 100;
const capturedMultipartByBoundary = new Map<
  string,
  Map<string, UploadedMockFile[]>
>();
const pendingFormDataCaptures: Map<string, UploadedMockFile[]>[] = [];
const installedRequestCtors = new WeakSet<typeof Request>();
const installedFormDataPrototypes = new WeakSet<FormData>();

function isFileLike(value: unknown): value is UploadedMockFile {
  if (typeof value !== 'object' || value === null) return false;
  const candidate = value as Partial<UploadedMockFile>;
  return (
    typeof candidate.name === 'string' &&
    typeof candidate.type === 'string' &&
    typeof candidate.size === 'number'
  );
}

function extractBoundary(contentType: string | null): string | null {
  if (!contentType) return null;
  const match = /boundary=(?:"([^"]+)"|([^;\s]+))/i.exec(contentType);
  return match?.[1] ?? match?.[2] ?? null;
}

function normalizeMime(mime: string): string {
  const lower = mime.toLowerCase();
  if (lower === '' || lower === 'application/octet-stream') return '';
  return lower;
}

function pushBoundedMap(
  map: Map<string, Map<string, UploadedMockFile[]>>,
  key: string,
  value: Map<string, UploadedMockFile[]>,
): void {
  if (map.size >= MAX_CAPTURED) {
    const oldest = map.keys().next().value;
    if (oldest !== undefined) {
      map.delete(oldest);
    }
  }
  map.set(key, value);
}

function pushCapturedFormData(byField: Map<string, UploadedMockFile[]>): void {
  if (byField.size === 0) return;
  if (pendingFormDataCaptures.length >= MAX_CAPTURED) {
    pendingFormDataCaptures.shift();
  }
  pendingFormDataCaptures.push(byField);
}

function collectFormDataFiles(
  formData: FormData,
  forEachFn: FormData['forEach'] = FormData.prototype.forEach,
): Map<string, UploadedMockFile[]> {
  const byField = new Map<string, UploadedMockFile[]>();
  forEachFn.call(formData, (value, key) => {
    if (!isFileLike(value)) return;
    const meta: UploadedMockFile = {
      name: value.name,
      type: value.type,
      size: value.size,
    };
    const list = byField.get(key);
    if (list) {
      list.push(meta);
    } else {
      byField.set(key, [meta]);
    }
  });
  return byField;
}

function stripOneTrailingCrlf(text: string): string {
  if (text.endsWith('\r\n')) return text.slice(0, -2);
  if (text.endsWith('\n')) return text.slice(0, -1);
  return text;
}

function parseRawMultipart(
  contentType: string,
  bodyBytes: Uint8Array,
  fieldName: string,
): UploadedMockFile[] {
  const boundary = extractBoundary(contentType);
  if (!boundary) return [];

  // Latin-1 preserves a 1:1 byte-to-character mapping so .length matches byte count.
  const raw = new TextDecoder('latin1').decode(bodyBytes);
  const parts = raw.split(`--${boundary}`);
  const files: UploadedMockFile[] = [];

  for (const part of parts) {
    if (part === '' || part.startsWith('--')) continue;
    let content = part;
    if (content.startsWith('\r\n')) {
      content = content.slice(2);
    } else if (content.startsWith('\n')) {
      content = content.slice(1);
    }

    let separatorIndex = content.indexOf('\r\n\r\n');
    let separatorLength = 4;
    if (separatorIndex === -1) {
      separatorIndex = content.indexOf('\n\n');
      separatorLength = 2;
    }
    if (separatorIndex === -1) continue;

    const headerBlock = content.slice(0, separatorIndex);
    const bodyText = stripOneTrailingCrlf(
      content.slice(separatorIndex + separatorLength),
    );

    const dispositionLine = headerBlock
      .split(/\r?\n/)
      .find((line) => line.toLowerCase().startsWith('content-disposition:'));
    if (!dispositionLine) continue;

    const nameMatch = /\bname="([^"]+)"/i.exec(dispositionLine);
    const fileNameMatch = /\bfilename="([^"]*)"/i.exec(dispositionLine);
    if (!nameMatch || nameMatch[1] !== fieldName || !fileNameMatch) continue;

    const typeLine = headerBlock
      .split(/\r?\n/)
      .find((line) => line.toLowerCase().startsWith('content-type:'));
    const mimeType = typeLine
      ? typeLine.slice('content-type:'.length).trim()
      : '';

    files.push({
      name: fileNameMatch[1],
      type: mimeType,
      size: bodyText.length > 0 ? bodyText.length : 1024,
    });
  }

  return files;
}

function matchPendingCapture(
  rawParts: UploadedMockFile[],
  fieldName: string,
): UploadedMockFile[] | null {
  for (let i = 0; i < pendingFormDataCaptures.length; i++) {
    const candidate = pendingFormDataCaptures[i]?.get(fieldName);
    if (!candidate || candidate.length !== rawParts.length) continue;
    const allMatch = candidate.every(
      (item, index) =>
        normalizeMime(item.type) === normalizeMime(rawParts[index].type),
    );
    if (allMatch) {
      pendingFormDataCaptures.splice(i, 1);
      return candidate.map((item) => ({ ...item }));
    }
  }
  return null;
}

function installJsdomMultipartCapture(): void {
  if (typeof globalThis === 'undefined' || !('jsdom' in globalThis)) return;

  // Hook FormData.prototype.forEach so Vitest's makeCompatFormData captures original
  // File metadata even when openapi-fetch cached Request before mocks were loaded.
  if (
    typeof FormData !== 'undefined' &&
    !installedFormDataPrototypes.has(FormData.prototype)
  ) {
    const originalForEach = FormData.prototype.forEach;
    FormData.prototype.forEach = function patchedForEach(callback, thisArg) {
      const captured = collectFormDataFiles(this, originalForEach);
      pushCapturedFormData(captured);
      return originalForEach.call(this, callback, thisArg);
    };
    installedFormDataPrototypes.add(FormData.prototype);
  }

  const OriginalRequest = globalThis.Request;
  if (!OriginalRequest || installedRequestCtors.has(OriginalRequest)) return;

  class MultipartCapturingRequest extends OriginalRequest {
    constructor(input: RequestInfo | URL, init?: RequestInit) {
      const body = init?.body;
      const captured =
        typeof FormData !== 'undefined' && body instanceof FormData
          ? collectFormDataFiles(body)
          : null;
      super(input, init);
      if (captured) {
        const boundary = extractBoundary(this.headers.get('content-type'));
        if (boundary) {
          pushBoundedMap(capturedMultipartByBoundary, boundary, captured);
        }
      }
    }
  }

  installedRequestCtors.add(OriginalRequest);
  installedRequestCtors.add(MultipartCapturingRequest);
  globalThis.Request = MultipartCapturingRequest;
}

installJsdomMultipartCapture();

/** Extracts uploaded file metadata safely under both browser FormData and Vitest jsdom. */
export async function readUploadedFiles(
  request: Request,
  fieldName = 'files',
): Promise<UploadedMockFile[]> {
  const contentType = request.headers.get('content-type');
  const boundary = extractBoundary(contentType);
  if (boundary) {
    const captured = capturedMultipartByBoundary.get(boundary);
    if (captured) {
      return [...(captured.get(fieldName) ?? [])];
    }
  }

  const clone = request.clone();
  try {
    const formData = await request.formData();
    const files: UploadedMockFile[] = [];
    for (const value of formData.getAll(fieldName)) {
      if (isFileLike(value)) {
        files.push({
          name: value.name,
          type: value.type,
          size: value.size,
        });
      }
    }
    return files;
  } catch {
    const bytes = new Uint8Array(await clone.arrayBuffer());
    const rawParts = parseRawMultipart(contentType ?? '', bytes, fieldName);
    if (rawParts.length === 0) return [];
    return matchPendingCapture(rawParts, fieldName) ?? rawParts;
  }
}
