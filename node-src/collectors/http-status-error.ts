/** HTTP failure with status retained separately from its operator-facing text. */
export class CollectorHttpStatusError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "CollectorHttpStatusError";
    this.status = status;
  }
}

export function collectorHttpStatus(error: unknown): number | null {
  return error instanceof CollectorHttpStatusError ? error.status : null;
}
