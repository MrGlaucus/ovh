import type { PurchaseHistory } from "@/hooks/use-history";

/**
 * 订单支付状态 → 标签。取值是 OVH 的 billing.order.OrderStatusEnum,三区一致。
 * 这才是用户真正关心的:「下单成功」只说明订单建了,付没付、过没过期,看这里。
 */
export function orderStatusView(item: Pick<PurchaseHistory, "orderStatus">): {
  label: string;
  tone: "success" | "warning" | "danger" | "info" | "progress" | "processing" | "default";
  paid: boolean;
  closed: boolean;
  title: string;
} {
  switch (item.orderStatus) {
    case "notPaid":
      return { label: "待付款", tone: "warning", paid: false, closed: false, title: "订单已创建,尚未付款;倒计时结束前未付款会作废" };
    case "checking":
      // 核验中改用青色：info 蓝和同列的「退款窗口倒计时」撞色；
      // 与交付中的紫、终态的绿都能拉开。
      return { label: "付款核验中", tone: "processing", paid: true, closed: false, title: "OVH 已收到付款,正在核验" };
    case "delivering":
      // 交付中≠已交付：都用绿色的话扫一遍列表分不出"还要等"和"已经到手"，
      // 交付中改用紫色：info 蓝和同列的「付款核验中」「退款窗口倒计时」撞色。
      return { label: "已付款·交付中", tone: "progress", paid: true, closed: false, title: "已付款,OVH 正在交付服务器" };
    case "delivered":
      return { label: "已付款·已交付", tone: "success", paid: true, closed: true, title: "已付款并交付" };
    case "cancelling":
      return { label: "取消中", tone: "danger", paid: false, closed: true, title: "订单正在取消" };
    case "cancelled":
      return { label: "已取消", tone: "danger", paid: false, closed: true, title: "订单已取消(过期未付款也会走到这里)" };
    case "documentsRequested":
      return { label: "需补材料", tone: "warning", paid: false, closed: false, title: "OVH 要求补充证件/材料后才处理" };
    case "unknown":
      return { label: "状态未知", tone: "default", paid: false, closed: false, title: "OVH 返回 unknown" };
    default:
      return { label: "状态未查到", tone: "default", paid: false, closed: false, title: "还没从 OVH 读到订单状态,点「刷新」再试" };
  }
}
