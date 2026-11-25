export function isChatId(value: string) {
  return /^[1-9]\d{0,18}$/.test(value) && (value.length < 19 || value <= "9223372036854775807");
}

export function isChatRegistration(value: string) {
  return /^reg:(?:\d{13}|\d{15})$/.test(value);
}

