<?php
require 'mddb.php';

$mddb = mddb::connect('localhost:11023','read');  // tryb klienta
$mddb = $mddb->collection('blog');
$mddb = $mddb->env('year','2004');

$homepage_content = $mddb->get('homepage','en_GB');  // %%year%% będzie podmienione

$posts = $mddb->search('category','blog','addedAt', true);

foreach ($posts as $post) {
  echo "<h2>" . htmlspecialchars($post->key) . "</h2>";
}
