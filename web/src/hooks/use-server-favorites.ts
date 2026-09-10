import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { toast } from "sonner";

export interface ServerFavorite {
  planCode: string;
  displayName: string;
  createdAt: string;
}

const favoriteKey = ["server-favorites"] as const;

export function useServerFavorites() {
  return useQuery({
    queryKey: favoriteKey,
    queryFn: async () => (await api.get<ServerFavorite[]>("/server-favorites")).data,
  });
}

export function useToggleServerFavorite() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ planCode, displayName, favorite }: { planCode: string; displayName: string; favorite: boolean }) => {
      if (favorite) await api.post("/server-favorites", { planCode, displayName });
      else await api.delete(`/server-favorites/${encodeURIComponent(planCode)}`);
    },
    onSuccess: (_, { favorite }) => {
      qc.invalidateQueries({ queryKey: favoriteKey });
      toast.success(favorite ? "已加入关注型号" : "已取消关注");
    },
    onError: (e: any) => toast.error(e.response?.data?.error || "更新收藏失败"),
  });
}
