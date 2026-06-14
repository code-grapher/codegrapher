export function Card(props: { label: string; count: number }) {
  return (
    <div className="card">
      <span className="label">{props.label}</span>
      <strong>{props.count}</strong>
    </div>
  );
}
