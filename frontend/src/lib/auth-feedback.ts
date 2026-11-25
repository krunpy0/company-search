export const authMessages = {
  invalid_input: "Проверьте email и длину пароля. Если используете латинские буквы и цифры, введите от 12 до 72 символов.",
  invalid_credentials: "Неверный email или пароль.",
  email_in_use: "Этот email уже зарегистрирован. Войдите в аккаунт.",
  busy: "Слишком много запросов. Попробуйте через несколько секунд.",
  unavailable: "Сервис входа временно недоступен. Попробуйте ещё раз.",
};

export function passwordFits(password: string) {
  const bytes = new TextEncoder().encode(password).length;
  return bytes >= 12 && bytes <= 72;
}

export function authMessage(value: unknown): string {
  if (typeof value === "object" && value && "code" in value) {
    switch (value.code) {
      case "invalid_input":
      case "invalid_credentials":
      case "email_in_use":
      case "busy":
        return authMessages[value.code];
    }
  }
  return authMessages.unavailable;
}
