export function Widget(props: { label: string; count: number }) {
  return (
    <div className="widget">
      <span className="label">{props.label}</span>
      <strong>{props.count}</strong>
    </div>
  );
}
