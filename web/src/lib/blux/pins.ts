export type PinTarget = {kind:'task'|'collaboration';id:string;pinned:boolean};
export function pinnedFirst<T extends {pinned_at?:string}>(items:T[],time:(item:T)=>string):T[]{
 return [...items].sort((a,b)=>(b.pinned_at||'').localeCompare(a.pinned_at||'')||time(b).localeCompare(time(a)));
}
