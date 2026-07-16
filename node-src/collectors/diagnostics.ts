/** Remove tenant-identifying vanity and customer values from diagnostics. */
export function maskCollectorIdentifiers(value: string): string {
  return value
    .replace(
      /(^|[/.])([^/.]+)(\.zslogin[a-z0-9]*\.net)/gi,
      (_match, prefix: string, _vanity: string, suffix: string) => {
        return `${prefix}<vanity>${suffix}`;
      },
    )
    .replace(/(\/customers\/)[^/?#]+/gi, "$1<customer-id>");
}
