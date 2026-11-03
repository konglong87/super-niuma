import { useCallback, useEffect, useRef, useState } from "react";
import { superError, WORK_ORDER_LIMIT, WORK_ORDER_STATUS, type SuperClient, type SuperError, type WorkOrder, type WorkOrderInput } from "./client";

export function useSuperWorkOrders(client: SuperClient | null) {
  const [orders, setOrders] = useState<WorkOrder[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<SuperError | null>(null);
  const [notice, setNotice] = useState<"saved" | "cancelledNotice" | null>(null);
  const generation = useRef(0);
  const inFlight = useRef(false);
  const attempt = useRef<{ fingerprint: string; key: string } | null>(null);

  const run = useCallback(async (operation: () => Promise<void>): Promise<void> => {
    if (inFlight.current) return;
    inFlight.current = true;
    const epoch = generation.current;
    setBusy(true); setError(null); setNotice(null);
    try { await operation(); }
    catch (failure) { if (epoch === generation.current) setError(superError(failure)); }
    finally { if (epoch === generation.current) { inFlight.current = false; setBusy(false); } }
  }, []);

  const refresh = useCallback(() => run(async () => {
    if (!client) return;
    const epoch = generation.current;
    const result = await client.list();
    if (epoch !== generation.current) return;
    setOrders(result ?? []);
    setSelectedID((current) => result?.some((order) => order.id === current) ? current : result?.[0]?.id ?? "");
  }), [client, run]);

  useEffect(() => {
    generation.current += 1;
    inFlight.current = false;
    setOrders([]); setSelectedID(""); setBusy(false); setError(null); setNotice(null);
    void refresh();
    return () => { generation.current += 1; };
  }, [refresh]);

  const create = (input: WorkOrderInput) => run(async () => {
    if (!client) return;
    const epoch = generation.current;
    const fingerprint = JSON.stringify(input);
    if (attempt.current?.fingerprint !== fingerprint) attempt.current = { fingerprint, key: crypto.randomUUID() };
    const order = await client.create(attempt.current.key, input);
    if (epoch !== generation.current) return;
    setOrders((current) => [order, ...current.filter((item) => item.id !== order.id)].slice(0, WORK_ORDER_LIMIT));
    setSelectedID(order.id);
    setNotice(order.status === WORK_ORDER_STATUS.cancelled ? "cancelledNotice" : "saved");
    // Keep the key after success too: accidental double submits remain idempotent.
  });

  const cancel = (id: string) => run(async () => {
    if (!client) return;
    const epoch = generation.current;
    const order = await client.cancel(id);
    if (epoch !== generation.current) return;
    setOrders((current) => current.map((item) => item.id === order.id ? order : item));
    setNotice("cancelledNotice");
  });
  return { orders, selected: orders.find((order) => order.id === selectedID), setSelectedID, busy, error, notice, refresh, create, cancel };
}
