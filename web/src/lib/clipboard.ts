/** HTTPS uses the Clipboard API; HTTP deployments and older mobile browsers
 * fall back to a selected textarea during the user's click interaction. */
export async function copyText(text: string): Promise<void> {
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // Permissions or embedded browser restrictions may block the modern API.
    }
  }

  const active = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const selection = window.getSelection();
  const ranges = selection
    ? Array.from({ length: selection.rangeCount }, (_, i) => selection.getRangeAt(i).cloneRange())
    : [];
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.readOnly = true;
  textarea.style.cssText = "position:fixed;top:0;left:0;width:1px;height:1px;opacity:0;font-size:16px;";
  document.body.appendChild(textarea);
  const onCopy = (event: ClipboardEvent) => {
    if (event.clipboardData) {
      event.clipboardData.setData("text/plain", text);
      event.preventDefault();
    }
  };
  document.addEventListener("copy", onCopy);
  try {
    textarea.focus({ preventScroll: true });
    textarea.select();
    textarea.setSelectionRange(0, text.length);
    if (!document.execCommand("copy")) throw new Error("Clipboard access denied");
  } finally {
    document.removeEventListener("copy", onCopy);
    textarea.remove();
    active?.focus({ preventScroll: true });
    if (selection) {
      selection.removeAllRanges();
      ranges.forEach((range) => selection.addRange(range));
    }
  }
}
