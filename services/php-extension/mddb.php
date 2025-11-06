<?php

class mddb {
  private string $base;
  private string $mode;
  private string $collection = '';
  private array $env = [];

  public static function connect(string $addr, string $mode = 'read'): self {
    $i = new self;
    $i->base = "http://$addr/v1";
    $i->mode = $mode;
    return $i;
  }

  public function collection(string $name): self {
    $this->collection = $name;
    return $this;
  }

  public function env(string $k, string $v): self {
    $this->env[$k] = $v;
    return $this;
  }

  public function get(string $key, string $lang) {
    $payload = [
      'collection' => $this->collection,
      'key' => $key,
      'lang' => $lang,
      'env' => $this->env
    ];
    return $this->post('/get', $payload);
  }

  public function add(string $key, string $lang, array $meta, string $contentMd) {
    if ($this->mode === 'read') throw new Exception("read-only client");
    $payload = [
      'collection' => $this->collection,
      'key' => $key,
      'lang' => $lang,
      'meta' => $meta,
      'contentMd' => $contentMd
    ];
    return $this->post('/add', $payload);
  }

  public function search(string $metaKey, string $metaVal, string $sort='addedAt', bool $asc=true, int $limit=100) {
    $payload = [
      'collection' => $this->collection,
      'filterMeta' => [ $metaKey => [$metaVal] ],
      'sort' => $sort,
      'asc' => $asc,
      'limit' => $limit,
      'offset' => 0
    ];
    return $this->post('/search', $payload);
  }

  private function post(string $path, array $payload) {
    $ch = curl_init($this->base . $path);
    curl_setopt($ch, CURLOPT_POST, true);
    curl_setopt($ch, CURLOPT_HTTPHEADER, ['Content-Type: application/json']);
    curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode($payload));
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    $res = curl_exec($ch);
    if ($res === false) throw new Exception(curl_error($ch));
    $code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    curl_close($ch);
    if ($code >= 400) throw new Exception($res);
    return json_decode($res);
  }
}
