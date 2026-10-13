// Same estimate as the Go daily reminder: purchase date + 15 days at 00:00 Beijing.
export function refundDeadlineMs(purchaseTime: string): number {
 const date = new Date(/(?:Z|[+-]\d{2}:?\d{2})$/i.test(purchaseTime) ? purchaseTime : `${purchaseTime}Z`);
 if (!Number.isFinite(date.getTime())) return 0;
 const beijing = new Date(date.getTime() + 8 * 3600000);
 return Date.UTC(beijing.getUTCFullYear(), beijing.getUTCMonth(), beijing.getUTCDate() + 15) - 8 * 3600000;
}
