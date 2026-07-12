const JSON_INTEGER_TOKEN = /^-?(?:0|[1-9][0-9]*)$/;
const JSON_NUMBER_TOKEN = /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$/;

/** Render one finite IEEE-754 value the way Python's `repr(float)` does. */
export function pythonFiniteFloatToken(value: number): string | null {
  if (!Number.isFinite(value)) {
    return null;
  }
  if (Object.is(value, -0)) {
    return "-0.0";
  }

  const sign = value < 0 ? "-" : "";
  const [mantissa = "", rawExponent = ""] = Math.abs(value)
    .toExponential()
    .split("e");
  const exponent = Number(rawExponent);
  const digits = mantissa.replace(".", "");
  if (!Number.isInteger(exponent) || !/^[0-9]+$/.test(digits)) {
    return null;
  }

  // CPython's shortest representation uses fixed notation for decimal
  // exponents in [-4, 15], and scientific notation everywhere else.
  if (exponent >= -4 && exponent < 16) {
    const point = exponent + 1;
    let body: string;
    if (point <= 0) {
      body = `0.${"0".repeat(-point)}${digits}`;
    } else if (point >= digits.length) {
      body = `${digits}${"0".repeat(point - digits.length)}.0`;
    } else {
      body = `${digits.slice(0, point)}.${digits.slice(point)}`;
    }
    return `${sign}${body}`;
  }

  const coefficient = digits.length === 1
    ? digits
    : `${digits[0]}.${digits.slice(1)}`;
  const exponentSign = exponent >= 0 ? "+" : "-";
  return `${sign}${coefficient}e${exponentSign}${String(Math.abs(exponent)).padStart(2, "0")}`;
}

/**
 * Canonicalize one losslessly parsed JSON number through Python's numeric
 * model: arbitrary-size integer tokens remain exact, while float tokens use
 * the finite binary64 value and spelling produced by `json.loads`/`json.dumps`.
 */
export function canonicalPythonNumberToken(token: string): string | null {
  if (JSON_INTEGER_TOKEN.test(token)) {
    return BigInt(token).toString(10);
  }
  if (!JSON_NUMBER_TOKEN.test(token)) {
    return null;
  }
  return pythonFiniteFloatToken(Number(token));
}
